// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ function.Function = SubFunction{}

// NewSubFunction returns a new instance of the cfncompat::sub provider-defined
// function, which implements CloudFormation's Fn::Sub semantics.
func NewSubFunction() function.Function {
	return SubFunction{}
}

// SubFunction implements the CloudFormation Fn::Sub intrinsic function as a
// Terraform provider-defined function.
type SubFunction struct{}

func (f SubFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "sub"
}

func (f SubFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary:     "Substitutes variables in an input string with values that you specify (CloudFormation Fn::Sub).",
		Description: "Replaces every ${VarName} occurrence in template with variables[\"VarName\"]. Write ${!VarName} to produce the literal text ${VarName} without performing a lookup. Substituted values are not re-scanned for further substitution.",
		MarkdownDescription: "Implements the [`Fn::Sub`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-sub.html) " +
			"intrinsic function.\n\n" +
			"Every `${VarName}` occurrence in `template` is replaced with `variables[\"VarName\"]`. " +
			"Write `${!VarName}` to produce the literal text `${VarName}` without performing a lookup " +
			"(the `!` is stripped and the rest of the substitution is left untouched). " +
			"Substituted values are **not** re-scanned for further `${...}` substitution (no recursion).\n\n" +
			"**Deviation from CloudFormation**: in a CloudFormation template, `${Name}` may also refer to a template " +
			"parameter name, a resource logical ID, or a resource attribute (`${MyInstance.PublicIp}`), which " +
			"CloudFormation resolves the same way as `Ref`/`Fn::GetAtt`. This provider function has no access to a " +
			"template's parameters/resources, so it only supports the explicit key-value `variables` map form of " +
			"`Fn::Sub`. Any such name must be resolved by the calling synthesis backend (e.g. CDK Terrain) into an " +
			"explicit entry of `variables` before calling this function — `${Name}` with no matching entry in " +
			"`variables` is an error.",
		Parameters: []function.Parameter{
			function.StringParameter{
				Name:                "template",
				MarkdownDescription: "The string containing `${VarName}` placeholders to substitute.",
			},
			function.MapParameter{
				ElementType:         types.StringType,
				Name:                "variables",
				MarkdownDescription: "A map of variable name to variable value used to resolve `${VarName}` placeholders in `template`.",
			},
		},
		Return: function.StringReturn{},
	}
}

func (f SubFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var template string
	var variables map[string]string

	if err := req.Arguments.Get(ctx, &template, &variables); err != nil {
		resp.Error = err
		return
	}

	result, err := subInterpolate(template, variables)
	if err != nil {
		resp.Error = function.NewArgumentFuncError(0, err.Error())
		return
	}

	resp.Error = resp.Result.Set(ctx, result)
}

// subInterpolate scans template for "${...}" placeholders and substitutes
// them per CloudFormation Fn::Sub semantics:
//   - "${!Name}" is an escape that produces the literal text "${Name}"
//     without performing any variable lookup.
//   - "${Name}" is replaced with variables["Name"]; if "Name" is not present
//     in variables, an error is returned.
//   - Substituted values are copied verbatim into the output and are never
//     re-scanned for further "${...}" substitution.
func subInterpolate(template string, variables map[string]string) (string, error) {
	var out strings.Builder
	out.Grow(len(template))

	remaining := template
	for {
		start := strings.Index(remaining, "${")
		if start == -1 {
			out.WriteString(remaining)
			break
		}

		out.WriteString(remaining[:start])

		afterOpen := remaining[start+2:]
		end := strings.Index(afterOpen, "}")
		if end == -1 {
			return "", fmt.Errorf("template contains an unterminated substitution: missing closing '}' for '${' at offset %d", len(template)-len(remaining)+start)
		}

		content := afterOpen[:end]
		remaining = afterOpen[end+1:]

		if strings.HasPrefix(content, "!") {
			// Escape: "${!Literal}" resolves to the literal text "${Literal}", no lookup performed.
			out.WriteString("${")
			out.WriteString(content[1:])
			out.WriteString("}")
			continue
		}

		if content == "" {
			return "", fmt.Errorf("template contains an empty substitution '${}': a variable name is required")
		}

		value, ok := variables[content]
		if !ok {
			return "", fmt.Errorf("template references variable %q which is not present in variables; in CloudFormation this could also be a template parameter, resource logical ID, or resource attribute, but this provider function only resolves entries of the variables map — the caller (e.g. CDK Terrain synthesis) must pre-resolve any such reference into an explicit variables entry", content)
		}

		out.WriteString(value)
	}

	return out.String(), nil
}
