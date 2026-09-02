// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ function.Function = IsDynamicReferenceFunction{}

// NewIsDynamicReferenceFunction returns a new instance of the
// provider::cfncompat::is_dynamic_reference function.
func NewIsDynamicReferenceFunction() function.Function {
	return IsDynamicReferenceFunction{}
}

// IsDynamicReferenceFunction reports whether a string is exactly one
// well-formed CloudFormation dynamic reference. It is the total counterpart of
// parse_dynamic_reference, which errors rather than returning null: a
// configuration that must branch on a plan-time value tests it here first.
type IsDynamicReferenceFunction struct{}

func (f IsDynamicReferenceFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "is_dynamic_reference"
}

func (f IsDynamicReferenceFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary:     "Report whether a string is one whole CloudFormation dynamic reference",
		Description: "Returns true when the argument is exactly one well-formed {{resolve:...}} CloudFormation dynamic reference, and false otherwise. Accepts exactly what parse_dynamic_reference accepts, but never fails.",
		MarkdownDescription: "Returns `true` when the argument is **exactly one** well-formed " +
			"[`{{resolve:...}}`](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references.html) " +
			"CloudFormation dynamic reference, and `false` otherwise -- including for a string that merely " +
			"contains one, or contains several.\n\n" +
			"It accepts exactly what `provider::cfncompat::parse_dynamic_reference` accepts, but never " +
			"fails, so a configuration that receives a plan-time string which *may or may not* be a dynamic " +
			"reference can branch on it before parsing:\n\n" +
			"```terraform\n" +
			"locals {\n" +
			"  is_ref = provider::cfncompat::is_dynamic_reference(var.image)\n" +
			"  parsed = local.is_ref ? provider::cfncompat::parse_dynamic_reference(var.image) : null\n" +
			"}\n" +
			"```",
		Parameters: []function.Parameter{
			function.StringParameter{
				Name:                "reference",
				MarkdownDescription: "The string to test.",
			},
		},
		Return: function.BoolReturn{},
	}
}

func (f IsDynamicReferenceFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var reference string

	resp.Error = function.ConcatFuncErrors(resp.Error, req.Arguments.Get(ctx, &reference))
	if resp.Error != nil {
		return
	}

	resp.Error = function.ConcatFuncErrors(resp.Error, resp.Result.Set(ctx, types.BoolValue(isDynamicReference(reference))))
}
