// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ function.Function = SplitFunction{}

// NewSplitFunction returns a new instance of the provider::cfncompat::split
// function, which implements CloudFormation's Fn::Split intrinsic function.
func NewSplitFunction() function.Function {
	return SplitFunction{}
}

// SplitFunction implements CloudFormation's Fn::Split intrinsic function:
// it splits a source string into a list of strings using a delimiter.
type SplitFunction struct{}

// Metadata sets the function name as it will be called from Terraform
// configuration, i.e. provider::cfncompat::split.
func (f SplitFunction) Metadata(_ context.Context, req function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "split"
}

// Definition sets the parameters, return type, and documentation for the
// split function.
func (f SplitFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary:     "Split a string into a list of strings using a delimiter",
		Description: "Splits a source string into a list of string values at each occurrence of a delimiter, mirroring CloudFormation's Fn::Split intrinsic function.",
		MarkdownDescription: "Splits `source` into a list of strings at each occurrence of `delimiter`, mirroring CloudFormation's " +
			"[`Fn::Split`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-split.html) intrinsic function.\n\n" +
			"Consecutive occurrences of the delimiter (or a delimiter at the start/end of `source`) produce empty string entries in the result, " +
			"e.g. `split(\"|\", \"a||c|\")` returns `[\"a\", \"\", \"c\", \"\"]`.\n\n" +
			"`delimiter` must be a non-empty string; CloudFormation does not document behavior for an empty delimiter, so this provider treats it as an error " +
			"rather than guessing at undocumented Go-specific splitting behavior.",
		Parameters: []function.Parameter{
			function.StringParameter{
				Name:                "delimiter",
				MarkdownDescription: "The string value that determines where the source string is divided. Must not be empty.",
			},
			function.StringParameter{
				Name:                "source",
				MarkdownDescription: "The string value to split.",
			},
		},
		Return: function.ListReturn{
			ElementType: types.StringType,
		},
	}
}

// Run implements the split function logic.
func (f SplitFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var delimiter string
	var source string

	resp.Error = function.ConcatFuncErrors(resp.Error, req.Arguments.Get(ctx, &delimiter, &source))
	if resp.Error != nil {
		return
	}

	if delimiter == "" {
		resp.Error = function.ConcatFuncErrors(resp.Error, function.NewArgumentFuncError(0, "split: delimiter must not be an empty string"))
		return
	}

	elements := splitSplitSource(delimiter, source)

	resp.Error = function.ConcatFuncErrors(resp.Error, resp.Result.Set(ctx, splitToStringList(ctx, elements)))
}

// splitSplitSource splits source on delimiter using standard Go semantics,
// which match CloudFormation's documented Fn::Split behavior (consecutive
// delimiters, and leading/trailing delimiters, produce empty string entries).
func splitSplitSource(delimiter, source string) []string {
	return strings.Split(source, delimiter)
}

// splitToStringList converts a []string into a types.List of types.String,
// suitable for use as the split function's result value.
func splitToStringList(ctx context.Context, elements []string) types.List {
	list, diags := types.ListValueFrom(ctx, types.StringType, elements)
	if diags.HasError() {
		// This should be unreachable: elements is always a []string, which is
		// always convertible to a types.List of types.String.
		return types.ListNull(types.StringType)
	}

	return list
}
