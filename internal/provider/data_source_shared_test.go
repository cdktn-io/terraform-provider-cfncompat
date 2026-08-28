// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
)

// Test helpers shared by every data source's unit tests: they drive a
// data source's Read the way the framework does, without a Terraform
// process.

// dataSourceSchema returns a data source's schema.
func dataSourceSchema(t *testing.T, d datasource.DataSource) dsschema.Schema {
	t.Helper()

	resp := &datasource.SchemaResponse{}
	d.Schema(context.Background(), datasource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics building schema: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// dataSourceConfig builds a tfsdk.Config from a data source model.
// tfsdk.Config is read-only, so the model is first written into a
// tfsdk.State (which shares the same value encoding) and its raw value is
// handed to the Config.
func dataSourceConfig(t *testing.T, schema dsschema.Schema, model any) tfsdk.Config {
	t.Helper()

	state := tfsdk.State{Schema: schema}
	diags := state.Set(context.Background(), model)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics setting config: %v", diags)
	}
	return tfsdk.Config{Schema: schema, Raw: state.Raw}
}

// dataSourceStateFromConfig returns the tfsdk.State the framework hands to a
// data source's Read: fwserver seeds ReadResponse.State from the
// configuration (so a Read that sets nothing echoes the config back), rather
// than from a wholly null value.
func dataSourceStateFromConfig(config tfsdk.Config) tfsdk.State {
	return tfsdk.State{Schema: config.Schema, Raw: config.Raw.Copy()}
}

// readDataSource runs a data source's Read with the given config model and
// returns the resulting state model plus the response. The state model is
// only read back when the Read succeeded, so a failed Read is inspected
// through the response's diagnostics.
func readDataSource[M any](t *testing.T, d datasource.DataSource, config M) (M, *datasource.ReadResponse) {
	t.Helper()

	ctx := context.Background()
	cfg := dataSourceConfig(t, dataSourceSchema(t, d), &config)
	resp := &datasource.ReadResponse{State: dataSourceStateFromConfig(cfg)}
	d.Read(ctx, datasource.ReadRequest{Config: cfg}, resp)

	var out M
	if !resp.Diagnostics.HasError() {
		if diags := resp.State.Get(ctx, &out); diags.HasError() {
			t.Fatalf("unexpected diagnostics reading state: %v", diags)
		}
	}
	return out, resp
}
