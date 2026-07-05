// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure FindInMapFunction satisfies the function.Function interface.
var _ function.Function = FindInMapFunction{}

// NewFindInMapFunction returns a new instance of the
// provider::cfncompat::find_in_map function.
func NewFindInMapFunction() function.Function {
	return FindInMapFunction{}
}

// FindInMapFunction implements CloudFormation's Fn::FindInMap intrinsic
// function: it returns the value corresponding to keys in a two-level map.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-findinmap.html
type FindInMapFunction struct{}

func (f FindInMapFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "find_in_map"
}

func (f FindInMapFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Look up a value in a two-level map",
		Description: "Returns the value corresponding to top_level_key and second_level_key in the two-level " +
			"mapping. If either key is not found and a default_value argument is supplied, the default is returned " +
			"instead; otherwise, a missing key is an error.",
		MarkdownDescription: "Returns the value corresponding to `top_level_key` and `second_level_key` in the " +
			"two-level `mapping`.\n\n" +
			"Matches the semantics of CloudFormation's " +
			"[`Fn::FindInMap`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-findinmap.html) " +
			"intrinsic function.\n\n" +
			"**Deviation from template-form CloudFormation:** in a CloudFormation template, `Fn::FindInMap`'s first " +
			"argument is the *logical name* of a mapping declared in the template's `Mappings` section. This " +
			"provider function has no `Mappings` section to resolve a name against, so it takes the mapping " +
			"*itself* as the first argument: `mapping` is a two-level object/map (`top_level_key` -> " +
			"`second_level_key` -> leaf value), where each leaf value is either a string or a list of strings, " +
			"matching the value shapes CloudFormation allows in a `Mappings` entry.\n\n" +
			"If `top_level_key` or `second_level_key` is not present in `mapping`, this function raises an error " +
			"naming the missing key and the level (top-level or second-level) it was expected at - unless a " +
			"`default_value` argument is supplied, in which case that default is returned instead. This mirrors " +
			"the optional `DefaultValue` parameter of CloudFormation's `Fn::FindInMap` (a standard parameter that " +
			"no longer requires the `AWS::LanguageExtensions` transform). `default_value` is variadic and " +
			"accepts zero or one arguments; supplying more than one is an error.",
		Parameters: []function.Parameter{
			function.DynamicParameter{
				Name: "mapping",
				Description: "The two-level mapping to search: top_level_key -> second_level_key -> value, where " +
					"each value is a string or a list of strings.",
				MarkdownDescription: "The two-level mapping to search: `top_level_key` -> `second_level_key` -> " +
					"value, where each value is a string or a list of strings. Accepts either an object or a map " +
					"as the underlying dynamic type, at both levels.",
			},
			function.StringParameter{
				Name:                "top_level_key",
				Description:         "The top-level key name to look up in mapping.",
				MarkdownDescription: "The top-level key name to look up in `mapping`.",
			},
			function.StringParameter{
				Name:                "second_level_key",
				Description:         "The second-level key name to look up in the entry selected by top_level_key.",
				MarkdownDescription: "The second-level key name to look up in the entry selected by `top_level_key`.",
			},
		},
		VariadicParameter: function.DynamicParameter{
			Name: "default_value",
			Description: "Optional fallback value returned if top_level_key or second_level_key is not found in " +
				"mapping. Zero or one arguments are accepted; supplying more than one is an error.",
			MarkdownDescription: "Optional fallback value returned if `top_level_key` or `second_level_key` is not " +
				"found in `mapping` (CloudFormation's standard `Fn::FindInMap` `DefaultValue` parameter). Zero or " +
				"one arguments are accepted; supplying more than one is an error. If omitted and a key is not " +
				"found, this function raises an error instead.",
		},
		Return: function.DynamicReturn{},
	}
}

func (f FindInMapFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var mapping types.Dynamic
	var topLevelKey types.String
	var secondLevelKey types.String
	var defaultValues types.Tuple

	resp.Error = req.Arguments.Get(ctx, &mapping, &topLevelKey, &secondLevelKey, &defaultValues)
	if resp.Error != nil {
		return
	}

	defaultElements := defaultValues.Elements()
	if len(defaultElements) > 1 {
		resp.Error = function.NewFuncError(fmt.Sprintf(
			"find_in_map: at most one default_value argument is allowed, got %d", len(defaultElements),
		))
		return
	}

	if mapping.IsUnknown() || mapping.IsUnderlyingValueUnknown() {
		return
	}

	if topLevelKey.IsUnknown() || secondLevelKey.IsUnknown() {
		return
	}

	for _, elem := range defaultElements {
		if elem.IsUnknown() {
			return
		}

		if dyn, ok := elem.(types.Dynamic); ok && dyn.IsUnderlyingValueUnknown() {
			return
		}
	}

	if mapping.IsNull() || mapping.IsUnderlyingValueNull() {
		resp.Error = function.NewArgumentFuncError(0, "find_in_map: mapping must not be null")
		return
	}

	if topLevelKey.IsNull() {
		resp.Error = function.NewArgumentFuncError(1, "find_in_map: top_level_key must not be null")
		return
	}

	if secondLevelKey.IsNull() {
		resp.Error = function.NewArgumentFuncError(2, "find_in_map: second_level_key must not be null")
		return
	}

	hasDefault := len(defaultElements) == 1

	var defaultValue attr.Value
	if hasDefault {
		defaultValue = defaultElements[0]
		if dyn, ok := defaultValue.(types.Dynamic); ok {
			defaultValue = dyn.UnderlyingValue()
		}
	}

	returnDefault := func() {
		result, err := findInMapValueToDynamic(ctx, defaultValue)
		if err != nil {
			resp.Error = function.NewFuncError(fmt.Sprintf("find_in_map: default_value: %s", err))
			return
		}

		resp.Error = resp.Result.Set(ctx, result)
	}

	topLevelEntries, err := findInMapEntries(mapping.UnderlyingValue())
	if err != nil {
		resp.Error = function.NewArgumentFuncError(0, fmt.Sprintf("find_in_map: mapping: %s", err))
		return
	}

	topEntry, ok := topLevelEntries[topLevelKey.ValueString()]
	if !ok {
		if hasDefault {
			returnDefault()
			return
		}

		resp.Error = function.NewArgumentFuncError(1, fmt.Sprintf(
			"find_in_map: top-level key %q not found in mapping", topLevelKey.ValueString(),
		))
		return
	}

	if topEntry.IsUnknown() {
		return
	}

	if topEntry.IsNull() {
		resp.Error = function.NewArgumentFuncError(1, fmt.Sprintf(
			"find_in_map: mapping[%q] must not be null", topLevelKey.ValueString(),
		))
		return
	}

	secondLevelEntries, err := findInMapEntries(topEntry)
	if err != nil {
		resp.Error = function.NewArgumentFuncError(1, fmt.Sprintf(
			"find_in_map: mapping[%q]: %s", topLevelKey.ValueString(), err,
		))
		return
	}

	secondEntry, ok := secondLevelEntries[secondLevelKey.ValueString()]
	if !ok {
		if hasDefault {
			returnDefault()
			return
		}

		resp.Error = function.NewArgumentFuncError(2, fmt.Sprintf(
			"find_in_map: second-level key %q not found in mapping[%q]", secondLevelKey.ValueString(), topLevelKey.ValueString(),
		))
		return
	}

	if secondEntry.IsUnknown() {
		return
	}

	if secondEntry.IsNull() {
		resp.Error = function.NewArgumentFuncError(2, fmt.Sprintf(
			"find_in_map: mapping[%q][%q] must not be null", topLevelKey.ValueString(), secondLevelKey.ValueString(),
		))
		return
	}

	result, err := findInMapValueToDynamic(ctx, secondEntry)
	if err != nil {
		resp.Error = function.NewArgumentFuncError(2, fmt.Sprintf(
			"find_in_map: mapping[%q][%q]: %s", topLevelKey.ValueString(), secondLevelKey.ValueString(), err,
		))
		return
	}

	resp.Error = resp.Result.Set(ctx, result)
}

// findInMapEntries returns the key/value entries of an attr.Value shaped as
// a mapping level - either a types.Object or a types.Map - as used at both
// the top level and second level of the "mapping" argument. It returns an
// error for any other underlying type.
func findInMapEntries(v attr.Value) (map[string]attr.Value, error) {
	if dyn, ok := v.(types.Dynamic); ok {
		v = dyn.UnderlyingValue()
	}

	switch val := v.(type) {
	case types.Object:
		return val.Attributes(), nil
	case types.Map:
		return val.Elements(), nil
	default:
		return nil, fmt.Errorf("expected an object or map, got %T", v)
	}
}

// findInMapValueToDynamic validates that a leaf value - either found in the
// mapping or supplied as default_value - is a string or a list of strings,
// matching the documented shape of a Fn::FindInMap mapping entry, and wraps
// it as the function's dynamic return value.
func findInMapValueToDynamic(ctx context.Context, v attr.Value) (types.Dynamic, error) {
	if dyn, ok := v.(types.Dynamic); ok {
		v = dyn.UnderlyingValue()
	}

	switch val := v.(type) {
	case types.String:
		return types.DynamicValue(val), nil
	case types.List:
		if !val.ElementType(ctx).Equal(types.StringType) {
			return types.Dynamic{}, fmt.Errorf(
				"value must be a string or list of strings, got list of %s", val.ElementType(ctx),
			)
		}

		return types.DynamicValue(val), nil
	case types.Tuple:
		for i, elemType := range val.ElementTypes(ctx) {
			if !elemType.Equal(types.StringType) {
				return types.Dynamic{}, fmt.Errorf(
					"value must be a string or list of strings, got a tuple with a non-string element at index %d (%s)",
					i, elemType,
				)
			}
		}

		return types.DynamicValue(val), nil
	default:
		return types.Dynamic{}, fmt.Errorf("value must be a string or list of strings, got %T", v)
	}
}
