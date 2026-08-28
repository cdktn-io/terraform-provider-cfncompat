// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	fwschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	rtresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// customResourceTestSchema returns the cfncompat_custom_resource schema.
func customResourceTestSchema(t *testing.T) fwschema.Schema {
	t.Helper()

	r := &CustomResource{}
	resp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics building schema: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// nullState returns a tfsdk.State with a null value, matching how the
// framework pre-populates CreateResponse.State before invoking Create.
func nullState(ctx context.Context, schema fwschema.Schema) tfsdk.State {
	return tfsdk.State{
		Schema: schema,
		Raw:    tftypes.NewValue(schema.Type().TerraformType(ctx), nil),
	}
}

// planFromModel builds a tfsdk.Plan from a CustomResourceModel.
func planFromModel(t *testing.T, schema fwschema.Schema, model CustomResourceModel) tfsdk.Plan {
	t.Helper()

	plan := tfsdk.Plan{Schema: schema}
	diags := plan.Set(context.Background(), &model)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics setting plan: %v", diags)
	}
	return plan
}

// stateFromModel builds a tfsdk.State from a CustomResourceModel.
func stateFromModel(t *testing.T, schema fwschema.Schema, model CustomResourceModel) tfsdk.State {
	t.Helper()

	state := tfsdk.State{Schema: schema}
	diags := state.Set(context.Background(), &model)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics setting state: %v", diags)
	}
	return state
}

// baseModel returns a CustomResourceModel with all Optional+Computed fields
// populated to their schema defaults, as they would be by the time Terraform
// hands a fully-resolved plan/state to Create/Update/Delete.
func baseModel() CustomResourceModel {
	return CustomResourceModel{
		ServiceToken:       types.StringValue(testLambdaServiceToken),
		ResourceType:       types.StringValue(customResourceDefaultResourceType),
		ResourceProperties: types.DynamicNull(),
		LogicalResourceId:  types.StringValue(customResourceDefaultLogicalResourceID),
		StackId:            types.StringValue(customResourceDefaultStackID),
		ServiceTimeout:     types.Int64Value(customResourceDefaultServiceTimeout),
		ResponseBucket:     types.StringValue("test-bucket"),
		ResponseKeyPrefix:  types.StringValue(""),
		Id:                 types.StringNull(),
		PhysicalResourceId: types.StringNull(),
		Data:               types.DynamicNull(),
	}
}

// mustDynamicObject builds a types.Dynamic wrapping an object with string
// attributes, for use as a resource_properties test fixture.
func mustDynamicObject(t *testing.T, attrs map[string]string) types.Dynamic {
	t.Helper()

	attrTypes := make(map[string]attr.Type, len(attrs))
	attrValues := make(map[string]attr.Value, len(attrs))
	for k, v := range attrs {
		attrTypes[k] = types.StringType
		attrValues[k] = types.StringValue(v)
	}

	obj, diags := types.ObjectValue(attrTypes, attrValues)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics building object: %v", diags)
	}
	return types.DynamicValue(obj)
}

// diagnosticsContain reports whether any diagnostic's summary or detail
// contains substr.
func diagnosticsContain(diags diag.Diagnostics, substr string) bool {
	for _, d := range diags {
		if strings.Contains(d.Summary(), substr) || strings.Contains(d.Detail(), substr) {
			return true
		}
	}
	return false
}

// newTestCustomResource builds a CustomResource with its engine wired to
// fresh fakes (bypassing Configure entirely, as the spec's mockable-engine
// design intends).
func newTestCustomResource(t *testing.T, handler crHandlerFunc) (*CustomResource, *fakeLambdaInvoker) {
	t.Helper()

	engine, invoker, _, _ := newTestEngine(t, handler)
	r := &CustomResource{engine: engine}
	return r, invoker
}

func TestCustomResourceCreate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	schema := customResourceTestSchema(t)

	t.Run("success sets id, physical_resource_id, and data", func(t *testing.T) {
		t.Parallel()

		r, _ := newTestCustomResource(t, func(event crEvent) crResponse {
			resp := successResponse(event)
			resp.PhysicalResourceId = "physical-1"
			resp.Data = map[string]any{
				"Key":    "Value",
				"Count":  3,
				"Nested": map[string]any{"Inner": []any{"a", "b"}},
			}
			return resp
		})

		model := baseModel()
		req := resource.CreateRequest{Plan: planFromModel(t, schema, model)}
		resp := &resource.CreateResponse{State: nullState(ctx, schema)}

		r.Create(ctx, req, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}

		var result CustomResourceModel
		if diags := resp.State.Get(ctx, &result); diags.HasError() {
			t.Fatalf("unexpected diagnostics reading state: %v", diags)
		}
		if result.Data.IsNull() || result.Data.IsUnknown() {
			t.Errorf("data = %v, want a known, non-null value", result.Data)
		}
		if result.Id.ValueString() != "physical-1" {
			t.Errorf("id = %q, want physical-1", result.Id.ValueString())
		}
		if result.PhysicalResourceId.ValueString() != "physical-1" {
			t.Errorf("physical_resource_id = %q, want physical-1", result.PhysicalResourceId.ValueString())
		}
	})

	t.Run("success without PhysicalResourceId defaults to RequestId", func(t *testing.T) {
		t.Parallel()

		r, invoker := newTestCustomResource(t, func(event crEvent) crResponse {
			return crResponse{Status: "SUCCESS"}
		})

		model := baseModel()
		req := resource.CreateRequest{Plan: planFromModel(t, schema, model)}
		resp := &resource.CreateResponse{State: nullState(ctx, schema)}

		r.Create(ctx, req, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}

		var result CustomResourceModel
		if diags := resp.State.Get(ctx, &result); diags.HasError() {
			t.Fatalf("unexpected diagnostics reading state: %v", diags)
		}
		requestID := invoker.lastCall().RequestId
		if result.PhysicalResourceId.ValueString() != requestID {
			t.Errorf("physical_resource_id = %q, want the generated RequestId %q", result.PhysicalResourceId.ValueString(), requestID)
		}
	})

	t.Run("failed writes no state", func(t *testing.T) {
		t.Parallel()

		r, _ := newTestCustomResource(t, func(event crEvent) crResponse {
			return crResponse{Status: "FAILED", Reason: "create exploded"}
		})

		model := baseModel()
		req := resource.CreateRequest{Plan: planFromModel(t, schema, model)}
		resp := &resource.CreateResponse{State: nullState(ctx, schema)}

		r.Create(ctx, req, resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostics, got none")
		}
		if !diagnosticsContain(resp.Diagnostics, "create exploded") {
			t.Errorf("diagnostics = %v, want them to mention the failure Reason", resp.Diagnostics)
		}
		if !resp.State.Raw.IsNull() {
			t.Error("expected no state to be written after a Create failure")
		}
	})

	t.Run("ServiceToken merged into request event ResourceProperties", func(t *testing.T) {
		t.Parallel()

		r, invoker := newTestCustomResource(t, func(event crEvent) crResponse {
			resp := successResponse(event)
			resp.PhysicalResourceId = "physical-1"
			return resp
		})

		model := baseModel()
		req := resource.CreateRequest{Plan: planFromModel(t, schema, model)}
		resp := &resource.CreateResponse{State: nullState(ctx, schema)}

		r.Create(ctx, req, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}

		event := invoker.lastCall()
		if event.ResourceProperties["ServiceToken"] != testLambdaServiceToken {
			t.Errorf("ResourceProperties[ServiceToken] = %v, want %q", event.ResourceProperties["ServiceToken"], testLambdaServiceToken)
		}
	})

	t.Run("resource_properties values are passed through", func(t *testing.T) {
		t.Parallel()

		r, invoker := newTestCustomResource(t, func(event crEvent) crResponse {
			resp := successResponse(event)
			resp.PhysicalResourceId = "physical-1"
			return resp
		})

		model := baseModel()
		model.ResourceProperties = mustDynamicObject(t, map[string]string{"Foo": "bar"})
		req := resource.CreateRequest{Plan: planFromModel(t, schema, model)}
		resp := &resource.CreateResponse{State: nullState(ctx, schema)}

		r.Create(ctx, req, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}

		event := invoker.lastCall()
		if event.ResourceProperties["Foo"] != "bar" {
			t.Errorf("ResourceProperties[Foo] = %v, want bar", event.ResourceProperties["Foo"])
		}
	})

	t.Run("missing response_bucket errors", func(t *testing.T) {
		t.Parallel()

		r, _ := newTestCustomResource(t, func(event crEvent) crResponse {
			return successResponse(event)
		})

		model := baseModel()
		model.ResponseBucket = types.StringNull()
		req := resource.CreateRequest{Plan: planFromModel(t, schema, model)}
		resp := &resource.CreateResponse{State: nullState(ctx, schema)}

		r.Create(ctx, req, resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic for a missing response_bucket, got none")
		}
	})

	t.Run("missing response_bucket falls back to provider bucket", func(t *testing.T) {
		t.Parallel()

		r, _ := newTestCustomResource(t, func(event crEvent) crResponse {
			resp := successResponse(event)
			resp.PhysicalResourceId = "physical-1"
			return resp
		})
		r.providerData = &ProviderData{CustomResourceBucket: "provider-bucket"}

		model := baseModel()
		model.ResponseBucket = types.StringNull()
		req := resource.CreateRequest{Plan: planFromModel(t, schema, model)}
		resp := &resource.CreateResponse{State: nullState(ctx, schema)}

		r.Create(ctx, req, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
	})

	t.Run("NoEcho true raises a warning diagnostic", func(t *testing.T) {
		t.Parallel()

		r, _ := newTestCustomResource(t, func(event crEvent) crResponse {
			resp := successResponse(event)
			resp.PhysicalResourceId = "physical-1"
			resp.NoEcho = true
			return resp
		})

		model := baseModel()
		req := resource.CreateRequest{Plan: planFromModel(t, schema, model)}
		resp := &resource.CreateResponse{State: nullState(ctx, schema)}

		r.Create(ctx, req, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected error diagnostics: %v", resp.Diagnostics)
		}
		if resp.Diagnostics.WarningsCount() == 0 {
			t.Error("expected a warning diagnostic for NoEcho=true")
		}
	})

	t.Run("resource not configured errors clearly", func(t *testing.T) {
		t.Parallel()

		r := &CustomResource{} // engine is nil: Configure never succeeded

		model := baseModel()
		req := resource.CreateRequest{Plan: planFromModel(t, schema, model)}
		resp := &resource.CreateResponse{State: nullState(ctx, schema)}

		r.Create(ctx, req, resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic when the engine was never configured, got none")
		}
	})
}

func TestCustomResourceUpdate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	schema := customResourceTestSchema(t)

	priorModel := baseModel()
	priorModel.PhysicalResourceId = types.StringValue("physical-old")
	priorModel.Id = types.StringValue("physical-old")

	t.Run("success same id", func(t *testing.T) {
		t.Parallel()

		r, invoker := newTestCustomResource(t, func(event crEvent) crResponse {
			return successResponse(event) // echoes the incoming PhysicalResourceId
		})

		plan := baseModel()
		req := resource.UpdateRequest{
			Plan:  planFromModel(t, schema, plan),
			State: stateFromModel(t, schema, priorModel),
		}
		resp := &resource.UpdateResponse{State: stateFromModel(t, schema, priorModel)}

		r.Update(ctx, req, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}

		var result CustomResourceModel
		if diags := resp.State.Get(ctx, &result); diags.HasError() {
			t.Fatalf("unexpected diagnostics reading state: %v", diags)
		}
		if result.PhysicalResourceId.ValueString() != "physical-old" {
			t.Errorf("physical_resource_id = %q, want physical-old (unchanged)", result.PhysicalResourceId.ValueString())
		}

		// Only one Invoke should have happened: no replacement cleanup Delete.
		if len(invoker.calls) != 1 {
			t.Errorf("expected exactly one dispatched event, got %d: %+v", len(invoker.calls), invoker.calls)
		}
	})

	t.Run("changed service_token: OldResourceProperties carries the OLD token", func(t *testing.T) {
		t.Parallel()

		const newServiceToken = "arn:aws:lambda:us-east-1:123456789012:function:new-handler"

		r, invoker := newTestCustomResource(t, func(event crEvent) crResponse {
			return successResponse(event) // echoes the incoming PhysicalResourceId
		})

		plan := baseModel()
		plan.ServiceToken = types.StringValue(newServiceToken)
		req := resource.UpdateRequest{
			Plan:  planFromModel(t, schema, plan),
			State: stateFromModel(t, schema, priorModel),
		}
		resp := &resource.UpdateResponse{State: stateFromModel(t, schema, priorModel)}

		r.Update(ctx, req, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}

		event := invoker.lastCall()
		if event.ResourceProperties["ServiceToken"] != newServiceToken {
			t.Errorf("ResourceProperties[ServiceToken] = %v, want the new token %q", event.ResourceProperties["ServiceToken"], newServiceToken)
		}
		if event.OldResourceProperties["ServiceToken"] != testLambdaServiceToken {
			t.Errorf("OldResourceProperties[ServiceToken] = %v, want the prior state's token %q, not the new one",
				event.OldResourceProperties["ServiceToken"], testLambdaServiceToken)
		}
	})

	t.Run("success new id sends a cleanup Delete with the OLD physical id and properties", func(t *testing.T) {
		t.Parallel()

		r, invoker := newTestCustomResource(t, func(event crEvent) crResponse {
			if event.RequestType == "Update" {
				resp := successResponse(event)
				resp.PhysicalResourceId = "physical-new"
				return resp
			}
			// The cleanup Delete.
			return successResponse(event)
		})

		priorWithProps := priorModel
		priorWithProps.ResourceProperties = mustDynamicObject(t, map[string]string{"Foo": "old-value"})

		plan := baseModel()

		req := resource.UpdateRequest{
			Plan:  planFromModel(t, schema, plan),
			State: stateFromModel(t, schema, priorWithProps),
		}
		resp := &resource.UpdateResponse{State: stateFromModel(t, schema, priorWithProps)}

		r.Update(ctx, req, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}

		var result CustomResourceModel
		if diags := resp.State.Get(ctx, &result); diags.HasError() {
			t.Fatalf("unexpected diagnostics reading state: %v", diags)
		}
		if result.PhysicalResourceId.ValueString() != "physical-new" {
			t.Errorf("physical_resource_id = %q, want physical-new", result.PhysicalResourceId.ValueString())
		}

		if len(invoker.calls) != 2 {
			t.Fatalf("expected 2 dispatched events (Update + cleanup Delete), got %d", len(invoker.calls))
		}
		cleanup := invoker.calls[1]
		if cleanup.RequestType != "Delete" {
			t.Errorf("cleanup event RequestType = %q, want Delete", cleanup.RequestType)
		}
		if cleanup.PhysicalResourceId != "physical-old" {
			t.Errorf("cleanup event PhysicalResourceId = %q, want physical-old", cleanup.PhysicalResourceId)
		}
		if cleanup.ResourceProperties["Foo"] != "old-value" {
			t.Errorf("cleanup event ResourceProperties[Foo] = %v, want old-value (the OLD properties)", cleanup.ResourceProperties["Foo"])
		}
	})

	t.Run("failed cleanup Delete is a warning, not an error", func(t *testing.T) {
		t.Parallel()

		r, _ := newTestCustomResource(t, func(event crEvent) crResponse {
			if event.RequestType == "Update" {
				resp := successResponse(event)
				resp.PhysicalResourceId = "physical-new"
				return resp
			}
			return crResponse{Status: "FAILED", Reason: "cleanup exploded"}
		})

		plan := baseModel()
		req := resource.UpdateRequest{
			Plan:  planFromModel(t, schema, plan),
			State: stateFromModel(t, schema, priorModel),
		}
		resp := &resource.UpdateResponse{State: stateFromModel(t, schema, priorModel)}

		r.Update(ctx, req, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected error diagnostics (cleanup failures must be warnings): %v", resp.Diagnostics)
		}
		if resp.Diagnostics.WarningsCount() == 0 {
			t.Error("expected a warning diagnostic for the failed cleanup delete")
		}

		// The Update's own new state must still have been recorded.
		var result CustomResourceModel
		if diags := resp.State.Get(ctx, &result); diags.HasError() {
			t.Fatalf("unexpected diagnostics reading state: %v", diags)
		}
		if result.PhysicalResourceId.ValueString() != "physical-new" {
			t.Errorf("physical_resource_id = %q, want physical-new (Update succeeded even though cleanup failed)", result.PhysicalResourceId.ValueString())
		}
	})

	t.Run("failed keeps prior state", func(t *testing.T) {
		t.Parallel()

		r, _ := newTestCustomResource(t, func(event crEvent) crResponse {
			return crResponse{Status: "FAILED", Reason: "update exploded"}
		})

		plan := baseModel()
		req := resource.UpdateRequest{
			Plan:  planFromModel(t, schema, plan),
			State: stateFromModel(t, schema, priorModel),
		}
		resp := &resource.UpdateResponse{State: stateFromModel(t, schema, priorModel)}

		r.Update(ctx, req, resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostics, got none")
		}

		var result CustomResourceModel
		if diags := resp.State.Get(ctx, &result); diags.HasError() {
			t.Fatalf("unexpected diagnostics reading state: %v", diags)
		}
		if result.PhysicalResourceId.ValueString() != "physical-old" {
			t.Errorf("physical_resource_id = %q, want physical-old (prior state preserved on failure)", result.PhysicalResourceId.ValueString())
		}
	})
}

func TestCustomResourceDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	schema := customResourceTestSchema(t)

	priorModel := baseModel()
	priorModel.PhysicalResourceId = types.StringValue("physical-old")
	priorModel.Id = types.StringValue("physical-old")

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		r, invoker := newTestCustomResource(t, func(event crEvent) crResponse {
			return successResponse(event)
		})

		req := resource.DeleteRequest{State: stateFromModel(t, schema, priorModel)}
		resp := &resource.DeleteResponse{State: stateFromModel(t, schema, priorModel)}

		r.Delete(ctx, req, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if invoker.lastCall().RequestType != "Delete" {
			t.Errorf("RequestType = %q, want Delete", invoker.lastCall().RequestType)
		}
		if invoker.lastCall().PhysicalResourceId != "physical-old" {
			t.Errorf("PhysicalResourceId = %q, want physical-old", invoker.lastCall().PhysicalResourceId)
		}
	})

	t.Run("failed keeps resource tracked", func(t *testing.T) {
		t.Parallel()

		r, _ := newTestCustomResource(t, func(event crEvent) crResponse {
			return crResponse{Status: "FAILED", Reason: "delete exploded"}
		})

		req := resource.DeleteRequest{State: stateFromModel(t, schema, priorModel)}
		resp := &resource.DeleteResponse{State: stateFromModel(t, schema, priorModel)}

		r.Delete(ctx, req, resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostics, got none")
		}
		if !diagnosticsContain(resp.Diagnostics, "delete exploded") {
			t.Errorf("diagnostics = %v, want them to mention the failure Reason", resp.Diagnostics)
		}
	})
}

func TestCustomResourceConfigure(t *testing.T) {
	t.Parallel()

	t.Run("nil ProviderData is a no-op", func(t *testing.T) {
		t.Parallel()

		r := &CustomResource{}
		resp := &resource.ConfigureResponse{}
		r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: nil}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if r.engine != nil {
			t.Error("expected engine to remain nil when ProviderData is nil")
		}
	})

	t.Run("ConfigErr surfaces as an error diagnostic", func(t *testing.T) {
		t.Parallel()

		r := &CustomResource{}
		resp := &resource.ConfigureResponse{}
		pd := &ProviderData{ConfigErr: errors.New("no credentials found")}
		r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: pd}, resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic when ProviderData.ConfigErr is set")
		}
		if r.engine != nil {
			t.Error("expected engine to remain nil when ConfigErr is set")
		}
	})

	t.Run("unexpected ProviderData type errors", func(t *testing.T) {
		t.Parallel()

		r := &CustomResource{}
		resp := &resource.ConfigureResponse{}
		r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "not-provider-data"}, resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic for an unexpected ProviderData type")
		}
	})
}

// TestCustomResourceValidateConfigStackID pins the ownership-key contract of
// RFC 006 section 2.3: an omitted stack_id silently becomes the shared
// "cfncompat/no-stack-id" sentinel, so it must warn (never error -- the
// default is kept so existing state does not churn), while a set value and a
// not-yet-known one must stay quiet.
func TestCustomResourceValidateConfigStackID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		stackID     types.String
		wantWarning bool
	}{
		{name: "null stack_id warns", stackID: types.StringNull(), wantWarning: true},
		{
			name:    "configured stack_id is quiet",
			stackID: types.StringValue("arn:aws:cloudformation:eu-west-1:123456789012:stack/MyApp-Prod/" + strings.Repeat("a", 8) + "-aaaa-5aaa-8aaa-" + strings.Repeat("a", 12)),
		},
		// An unknown value is a data source that has not been read yet --
		// perfectly good wiring, just unresolved at validate time.
		{name: "unknown stack_id is quiet", stackID: types.StringUnknown()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			schema := customResourceTestSchema(t)
			model := baseModel()
			model.StackId = tt.stackID

			// tfsdk.Config is read-only, so the model goes through a
			// tfsdk.Plan (same value encoding) and its raw value is handed
			// to the Config.
			config := tfsdk.Config{Schema: schema, Raw: planFromModel(t, schema, model).Raw}

			r := &CustomResource{}
			resp := &resource.ValidateConfigResponse{}
			r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: config}, resp)

			if resp.Diagnostics.HasError() {
				t.Fatalf("ValidateConfig must never error, got: %v", resp.Diagnostics)
			}
			if got := resp.Diagnostics.WarningsCount() > 0; got != tt.wantWarning {
				t.Fatalf("warning emitted = %v, want %v (diags: %v)", got, tt.wantWarning, resp.Diagnostics)
			}
			if !tt.wantWarning {
				return
			}
			if !diagnosticsContain(resp.Diagnostics, "stack_id not set") {
				t.Errorf("diagnostics %v do not carry the expected summary", resp.Diagnostics)
			}
			if !diagnosticsContain(resp.Diagnostics, customResourceDefaultStackID) {
				t.Errorf("diagnostics %v do not name the shared sentinel", resp.Diagnostics)
			}
			if !diagnosticsContain(resp.Diagnostics, "data.cfncompat_pseudo_parameters") {
				t.Errorf("diagnostics %v do not recommend the pseudo-parameters wiring", resp.Diagnostics)
			}
		})
	}
}

func TestCustomResourceServiceTokenValidator(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"lambda arn", testLambdaServiceToken, false},
		{"sns arn", testSNSServiceToken, false},
		{"sqs arn is rejected", "arn:aws:sqs:us-east-1:123456789012:my-queue", true},
		{"not an arn", "not-an-arn", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := validator.StringRequest{Path: path.Root("service_token"), ConfigValue: types.StringValue(tc.value)}
			resp := &validator.StringResponse{}
			customResourceServiceTokenValidator{}.ValidateString(context.Background(), req, resp)
			if got := resp.Diagnostics.HasError(); got != tc.wantErr {
				t.Errorf("HasError() = %v, want %v (diags: %v)", got, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestCustomResourceTypeValidator(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"default type", customResourceDefaultResourceType, false},
		{"valid custom type", "Custom::MyResource", false},
		{"missing Custom:: prefix", "MyResource", true},
		{"invalid name characters", "Custom::My Resource!", true},
		{"too long", "Custom::" + strings.Repeat("a", 60), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := validator.StringRequest{Path: path.Root("resource_type"), ConfigValue: types.StringValue(tc.value)}
			resp := &validator.StringResponse{}
			customResourceTypeValidator{}.ValidateString(context.Background(), req, resp)
			if got := resp.Diagnostics.HasError(); got != tc.wantErr {
				t.Errorf("HasError() = %v, want %v (diags: %v)", got, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestCustomResourceServiceTimeoutValidator(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		value   int64
		wantErr bool
	}{
		{"min", 1, false},
		{"max", 3600, false},
		{"default", customResourceDefaultServiceTimeout, false},
		{"zero", 0, true},
		{"negative", -1, true},
		{"too large", 3601, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := customResourceInt64RangeValidator{min: 1, max: 3600}
			req := validator.Int64Request{Path: path.Root("service_timeout"), ConfigValue: types.Int64Value(tc.value)}
			resp := &validator.Int64Response{}
			v.ValidateInt64(context.Background(), req, resp)
			if got := resp.Diagnostics.HasError(); got != tc.wantErr {
				t.Errorf("HasError() = %v, want %v (diags: %v)", got, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

// TestAccCustomResource is gated on TF_ACC plus two additional environment
// variables, since it exercises real AWS Lambda + S3. Skips otherwise.
//
// Required handler behavior for CFNCOMPAT_TEST_LAMBDA_ARN: a completely
// standard CloudFormation custom-resource handler — it receives the CFN
// event and PUTs its JSON response (Status, Reason, PhysicalResourceId,
// StackId, RequestId, LogicalResourceId, Data) to event.ResponseURL (a
// pre-signed S3 URL under CFNCOMPAT_TEST_RESPONSE_BUCKET). The test
// expects the handler to respond SUCCESS and echo
// ResourceProperties.Greeting back as Data.Echo.
//
// AWS credentials/region come from the environment (e.g. via
// aws-vault exec). See RFCs/005-custom-resource-polyfill.md.
func TestAccCustomResource(t *testing.T) {
	lambdaARN := os.Getenv("CFNCOMPAT_TEST_LAMBDA_ARN")
	responseBucket := os.Getenv("CFNCOMPAT_TEST_RESPONSE_BUCKET")
	if lambdaARN == "" || responseBucket == "" {
		t.Skip("set CFNCOMPAT_TEST_LAMBDA_ARN and CFNCOMPAT_TEST_RESPONSE_BUCKET to run this acceptance test")
	}

	config := func(greeting string) string {
		return `
provider "cfncompat" {
  custom_resource_bucket = "` + responseBucket + `"
}

resource "cfncompat_custom_resource" "test" {
  service_token       = "` + lambdaARN + `"
  resource_type       = "Custom::CfncompatAccTest"
  logical_resource_id = "CfncompatAccTestResource"
  service_timeout     = 120

  resource_properties = {
    Greeting = "` + greeting + `"
  }
}

output "echo" {
  value = cfncompat_custom_resource.test.data.Echo
}
`
	}

	rtresource.Test(t, rtresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []rtresource.TestStep{
			{
				// Create.
				Config: config("hello"),
				Check: rtresource.ComposeAggregateTestCheckFunc(
					rtresource.TestCheckResourceAttrSet("cfncompat_custom_resource.test", "physical_resource_id"),
					rtresource.TestCheckOutput("echo", "hello"),
				),
			},
			{
				// Update in place (same physical id from the echo handler).
				Config: config("updated"),
				Check: rtresource.ComposeAggregateTestCheckFunc(
					rtresource.TestCheckResourceAttrSet("cfncompat_custom_resource.test", "physical_resource_id"),
					rtresource.TestCheckOutput("echo", "updated"),
				),
			},
			// Destroy (Delete event) runs automatically at the end.
		},
	})
}
