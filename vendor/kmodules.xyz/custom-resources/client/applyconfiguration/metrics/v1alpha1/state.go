/*
Copyright AppsCode Inc. and Contributors

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

// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1alpha1

// StateApplyConfiguration represents an declarative configuration of the State type for use
// with apply.
type StateApplyConfiguration struct {
	LabelKey *string                         `json:"labelKey,omitempty"`
	Values   []StateValuesApplyConfiguration `json:"values,omitempty"`
}

// StateApplyConfiguration constructs an declarative configuration of the State type for use with
// apply.
func State() *StateApplyConfiguration {
	return &StateApplyConfiguration{}
}

// WithLabelKey sets the LabelKey field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the LabelKey field is set to the value of the last call.
func (b *StateApplyConfiguration) WithLabelKey(value string) *StateApplyConfiguration {
	b.LabelKey = &value
	return b
}

// WithValues adds the given value to the Values field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Values field.
func (b *StateApplyConfiguration) WithValues(values ...*StateValuesApplyConfiguration) *StateApplyConfiguration {
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithValues")
		}
		b.Values = append(b.Values, *values[i])
	}
	return b
}
