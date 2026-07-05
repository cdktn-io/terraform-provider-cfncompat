//go:build ignore

// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

// PLACEHOLDER: Future resource cfncompat_custom_resource
//
// This file is a reference skeleton for the planned cfncompat_custom_resource,
// which will enable CloudFormation Custom Resource-style deployment semantics
// (pre-signed S3 URL-backed Lambda invocations with CREATE/UPDATE/DELETE
// lifecycle and response polling) within CDK Terrain's synthesis backend.
//
// To implement:
//   1. Remove the //go:build ignore line above
//   2. Register in provider.go Resources() via NewCustomResource()
//   3. Write unit and acceptance tests

package provider

/*
import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &CustomResource{}
var _ resource.ResourceWithImportState = &CustomResource{}

func NewCustomResource() resource.Resource {
	return &CustomResource{}
}

type CustomResource struct{}

type CustomResourceModel struct {
	ServiceToken  types.String `tfsdk:"service_token"`
	Id            types.String `tfsdk:"id"`
	// ... additional CloudFormation Custom Resource fields
}

func (r *CustomResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_custom_resource"
}

func (r *CustomResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	// TODO: define schema matching CloudFormation Custom Resource contract
}

func (r *CustomResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// TODO: send pre-signed S3 URL to Lambda, poll for SUCCESS
}

func (r *CustomResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// TODO: read custom resource state
}

func (r *CustomResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// TODO: send UPDATE event
}

func (r *CustomResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// TODO: send DELETE event
}

func (r *CustomResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
*/
