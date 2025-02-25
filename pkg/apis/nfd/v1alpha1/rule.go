/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"k8s.io/klog/v2"

	"sigs.k8s.io/node-feature-discovery/pkg/utils"
)

// RuleOutput contains the output out rule execution.
// +k8s:deepcopy-gen=false
type RuleOutput struct {
	Labels map[string]string
	Vars   map[string]string
}

// Execute the rule against a set of input features.
func (r *Rule) Execute(features *Features) (RuleOutput, error) {
	labels := make(map[string]string)
	vars := make(map[string]string)

	if len(r.MatchAny) > 0 {
		// Logical OR over the matchAny matchers
		matched := false
		for _, matcher := range r.MatchAny {
			if isMatch, matches, err := matcher.match(features); err != nil {
				return RuleOutput{}, err
			} else if isMatch {
				matched = true
				utils.KlogDump(4, "matches for matchAny "+r.Name, "  ", matches)

				if r.LabelsTemplate == "" && r.VarsTemplate == "" {
					// there's no need to evaluate other matchers in MatchAny
					// if there are no templates to be executed on them - so
					// short-circuit and stop on first match here
					break
				}

				if err := r.executeLabelsTemplate(matches, labels); err != nil {
					return RuleOutput{}, err
				}
				if err := r.executeVarsTemplate(matches, vars); err != nil {
					return RuleOutput{}, err
				}
			}
		}
		if !matched {
			klog.V(2).Infof("rule %q did not match", r.Name)
			return RuleOutput{}, nil
		}
	}

	if len(r.MatchFeatures) > 0 {
		if isMatch, matches, err := r.MatchFeatures.match(features); err != nil {
			return RuleOutput{}, err
		} else if !isMatch {
			klog.V(2).Infof("rule %q did not match", r.Name)
			return RuleOutput{}, nil
		} else {
			utils.KlogDump(4, "matches for matchFeatures "+r.Name, "  ", matches)
			if err := r.executeLabelsTemplate(matches, labels); err != nil {
				return RuleOutput{}, err
			}
			if err := r.executeVarsTemplate(matches, vars); err != nil {
				return RuleOutput{}, err
			}
		}
	}

	for k, v := range r.Labels {
		labels[k] = v
	}
	for k, v := range r.Vars {
		vars[k] = v
	}

	ret := RuleOutput{Labels: labels, Vars: vars}
	utils.KlogDump(2, fmt.Sprintf("rule %q matched with: ", r.Name), "  ", ret)

	return ret, nil
}

func (r *Rule) executeLabelsTemplate(in matchedFeatures, out map[string]string) error {
	if r.LabelsTemplate == "" {
		return nil
	}

	if r.labelsTemplate == nil {
		t, err := newTemplateHelper(r.LabelsTemplate)
		if err != nil {
			return fmt.Errorf("failed to parse LabelsTemplate: %w", err)
		}
		r.labelsTemplate = t
	}

	labels, err := r.labelsTemplate.expandMap(in)
	if err != nil {
		return fmt.Errorf("failed to expand LabelsTemplate: %w", err)
	}
	for k, v := range labels {
		out[k] = v
	}
	return nil
}

func (r *Rule) executeVarsTemplate(in matchedFeatures, out map[string]string) error {
	if r.VarsTemplate == "" {
		return nil
	}
	if r.varsTemplate == nil {
		t, err := newTemplateHelper(r.VarsTemplate)
		if err != nil {
			return err
		}
		r.varsTemplate = t
	}

	vars, err := r.varsTemplate.expandMap(in)
	if err != nil {
		return err
	}
	for k, v := range vars {
		out[k] = v
	}
	return nil
}

type matchedFeatures map[string]domainMatchedFeatures

type domainMatchedFeatures map[string]interface{}

func (e *MatchAnyElem) match(features *Features) (bool, matchedFeatures, error) {
	return e.MatchFeatures.match(features)
}

func (m *FeatureMatcher) match(features *Features) (bool, matchedFeatures, error) {
	matches := make(matchedFeatures, len(*m))

	// Logical AND over the terms
	for _, term := range *m {
		// Ignore case
		featureName := strings.ToLower(term.Feature)

		nameSplit := strings.SplitN(term.Feature, ".", 2)
		if len(nameSplit) != 2 {
			klog.Warning("feature %q not of format <domain>.<feature>, cannot be used for templating", term.Feature)
			nameSplit = []string{featureName, ""}
		}

		if _, ok := matches[nameSplit[0]]; !ok {
			matches[nameSplit[0]] = make(domainMatchedFeatures)
		}

		var isMatch bool
		var err error
		if f, ok := features.Flags[featureName]; ok {
			m, v, e := term.MatchExpressions.MatchGetKeys(f.Elements)
			isMatch = m
			err = e
			matches[nameSplit[0]][nameSplit[1]] = v
		} else if f, ok := features.Attributes[featureName]; ok {
			m, v, e := term.MatchExpressions.MatchGetValues(f.Elements)
			isMatch = m
			err = e
			matches[nameSplit[0]][nameSplit[1]] = v
		} else if f, ok := features.Instances[featureName]; ok {
			v, e := term.MatchExpressions.MatchGetInstances(f.Elements)
			isMatch = len(v) > 0
			err = e
			matches[nameSplit[0]][nameSplit[1]] = v
		} else {
			return false, nil, fmt.Errorf("feature %q not available", featureName)
		}

		if err != nil {
			return false, nil, err
		} else if !isMatch {
			return false, nil, nil
		}
	}
	return true, matches, nil
}

type templateHelper struct {
	template *template.Template
}

func newTemplateHelper(name string) (*templateHelper, error) {
	tmpl, err := template.New("").Option("missingkey=error").Parse(name)
	if err != nil {
		return nil, fmt.Errorf("invalid template: %w", err)
	}
	return &templateHelper{template: tmpl}, nil
}

// DeepCopy is a stub to augment the auto-generated code
func (h *templateHelper) DeepCopy() *templateHelper {
	if h == nil {
		return nil
	}
	out := new(templateHelper)
	h.DeepCopyInto(out)
	return out
}

// DeepCopyInto is a stub to augment the auto-generated code
func (h *templateHelper) DeepCopyInto(out *templateHelper) {
	// HACK: just re-use the template
	out.template = h.template
}

func (h *templateHelper) execute(data interface{}) (string, error) {
	var tmp bytes.Buffer
	if err := h.template.Execute(&tmp, data); err != nil {
		return "", err
	}
	return tmp.String(), nil
}

// expandMap is a helper for expanding a template in to a map of strings. Data
// after executing the template is expexted to be key=value pairs separated by
// newlines.
func (h *templateHelper) expandMap(data interface{}) (map[string]string, error) {
	expanded, err := h.execute(data)
	if err != nil {
		return nil, err
	}

	// Split out individual key-value pairs
	out := make(map[string]string)
	for _, item := range strings.Split(expanded, "\n") {
		// Remove leading/trailing whitespace and skip empty lines
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			split := strings.SplitN(trimmed, "=", 2)
			if len(split) == 1 {
				return nil, fmt.Errorf("missing value in expanded template line %q, (format must be '<key>=<value>')", trimmed)
			}
			out[split[0]] = split[1]
		}
	}
	return out, nil
}
