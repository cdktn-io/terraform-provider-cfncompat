// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/base64"

	"github.com/hashicorp/terraform-plugin-framework/function"
)

var _ function.Function = Base64Function{}

// NewBase64Function returns a new instance of the cfncompat::base64
// provider-defined function, implementing CloudFormation's Fn::Base64
// intrinsic function.
func NewBase64Function() function.Function {
	return Base64Function{}
}

// Base64Function implements CloudFormation's Fn::Base64 intrinsic function:
// it returns the Base64 representation of the input string.
type Base64Function struct{}

func (r Base64Function) Metadata(_ context.Context, req function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "base64"
}

func (r Base64Function) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Returns the Base64 representation of a string, matching CloudFormation's Fn::Base64.",
		MarkdownDescription: "Returns the Base64 representation of the input string, matching the semantics of " +
			"CloudFormation's [`Fn::Base64`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-base64.html) " +
			"intrinsic function. This is typically used to pass encoded data (e.g. EC2 `UserData`) through Terraform " +
			"configuration that mirrors a CloudFormation template.",
		Parameters: []function.Parameter{
			function.StringParameter{
				Name:                "content",
				MarkdownDescription: "The string value to convert to Base64 (CloudFormation's `valueToEncode` parameter).",
			},
		},
		Return: function.StringReturn{},
	}
}

func (r Base64Function) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var content string

	resp.Error = function.ConcatFuncErrors(resp.Error, req.Arguments.Get(ctx, &content))
	if resp.Error != nil {
		return
	}

	encoded := base64Encode(content)

	resp.Error = function.ConcatFuncErrors(resp.Error, resp.Result.Set(ctx, encoded))
}

// base64Encode returns the standard Base64 encoding of s, matching
// CloudFormation's Fn::Base64 behavior.
func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
