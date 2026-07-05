// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"os"
	"testing"

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

// testAccPreCheck validates that TF_ACC is set, skipping the test otherwise
// (terraform-plugin-testing's resource.Test/UnitTest already do this, but
// tests that build their own acceptance scaffolding, like
// TestAccCustomResource, call this directly). Individual acceptance tests
// may layer additional environment variable requirements (e.g.
// CFNCOMPAT_TEST_LAMBDA_ARN) on top of this.
func testAccPreCheck(t *testing.T) {
	t.Helper()

	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance tests are skipped unless TF_ACC is set")
	}
}
