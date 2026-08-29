// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	provschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// testAccProtoV6ProviderFactories is used to instantiate a provider during
// acceptance testing. The factory function is called for each Terraform CLI
// command to create a provider server that the CLI can connect to and interact
// with.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"cfncompat": providerserver.NewProtocol6WithError(New("test")()),
}

// TestProviderSchemaEndpoints pins the set of overridable service endpoints,
// so a new AWS client added to a resource or data source cannot forget its
// LocalStack override (ec2 is the availability-zones data source's).
func TestProviderSchemaEndpoints(t *testing.T) {
	t.Parallel()

	resp := &provider.SchemaResponse{}
	New("test")().Schema(context.Background(), provider.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics building the provider schema: %v", resp.Diagnostics)
	}

	endpoints, ok := resp.Schema.Attributes["endpoints"].(provschema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("endpoints is %T, want a SingleNestedAttribute", resp.Schema.Attributes["endpoints"])
	}

	want := []string{"lambda", "sns", "s3", "sts", "ec2"}
	if got := len(endpoints.Attributes); got != len(want) {
		t.Errorf("endpoints has %d attributes, want %d: %v", got, len(want), endpoints.Attributes)
	}
	for _, name := range want {
		attribute, ok := endpoints.Attributes[name]
		if !ok {
			t.Errorf("endpoints.%s is missing", name)
			continue
		}
		if !attribute.IsOptional() {
			t.Errorf("endpoints.%s must be optional", name)
		}
		if attribute.GetDescription() == "" {
			t.Errorf("endpoints.%s has no description", name)
		}
	}
}
