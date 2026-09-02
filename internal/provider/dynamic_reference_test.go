// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"strings"
	"testing"
)

func TestParseDynamicReference(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input         string
		want          parsedDynamicReference
		expectError   bool
		errorContains string
	}{
		// --- ssm -----------------------------------------------------------
		"doc example: public AMI parameter": {
			input: "{{resolve:ssm:/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-6.1-x86_64}}",
			want: parsedDynamicReference{
				Service: dynamicReferenceServiceSSM,
				Name:    "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-6.1-x86_64",
			},
		},
		"doc example: custom AMI parameter with version": {
			input: "{{resolve:ssm:golden-ami:2}}",
			want:  parsedDynamicReference{Service: dynamicReferenceServiceSSM, Name: "golden-ami", Version: "2"},
		},
		"ssm name with dots, underscores and dashes": {
			input: "{{resolve:ssm:my_param.v1-final}}",
			want:  parsedDynamicReference{Service: dynamicReferenceServiceSSM, Name: "my_param.v1-final"},
		},
		"ssm multi-digit version": {
			input: "{{resolve:ssm:/a/b:1234}}",
			want:  parsedDynamicReference{Service: dynamicReferenceServiceSSM, Name: "/a/b", Version: "1234"},
		},
		"ssm label is rejected: CloudFormation supports no labels in dynamic references": {
			input:         "{{resolve:ssm:/a/b:prod}}",
			expectError:   true,
			errorContains: "Parameter labels are not supported in dynamic references",
		},
		"ssm with no parameter name": {
			input:         "{{resolve:ssm:}}",
			expectError:   true,
			errorContains: "names no parameter",
		},
		"ssm name with an illegal character": {
			input:         "{{resolve:ssm:my param}}",
			expectError:   true,
			errorContains: "contains characters CloudFormation does not allow",
		},

		// --- ssm-secure ----------------------------------------------------
		"doc example: ssm-secure with version": {
			input: "{{resolve:ssm-secure:IAMUserPassword:10}}",
			want:  parsedDynamicReference{Service: dynamicReferenceServiceSSMSecure, Name: "IAMUserPassword", Version: "10"},
		},
		"ssm-secure without version": {
			input: "{{resolve:ssm-secure:/db/password}}",
			want:  parsedDynamicReference{Service: dynamicReferenceServiceSSMSecure, Name: "/db/password"},
		},

		// --- secretsmanager ------------------------------------------------
		"doc example: whole SecretString": {
			input: "{{resolve:secretsmanager:MySecret}}",
			want:  parsedDynamicReference{Service: dynamicReferenceServiceSecretsManager, Name: "MySecret"},
		},
		"doc example: whole SecretString with all segments empty": {
			input: "{{resolve:secretsmanager:MySecret::::}}",
			want:  parsedDynamicReference{Service: dynamicReferenceServiceSecretsManager, Name: "MySecret"},
		},
		"doc example: json key": {
			input: "{{resolve:secretsmanager:MySecret:SecretString:password}}",
			want: parsedDynamicReference{
				Service: dynamicReferenceServiceSecretsManager, Name: "MySecret",
				SecretString: "SecretString", JSONKey: "password",
			},
		},
		"doc example: json key of a specific version stage": {
			input: "{{resolve:secretsmanager:MySecret:SecretString:password:AWSPREVIOUS}}",
			want: parsedDynamicReference{
				Service: dynamicReferenceServiceSecretsManager, Name: "MySecret",
				SecretString: "SecretString", JSONKey: "password", VersionStage: "AWSPREVIOUS",
			},
		},
		"version id in the last position": {
			input: "{{resolve:secretsmanager:MySecret:SecretString:password::01234567-89ab-cdef-0123-456789abcdef}}",
			want: parsedDynamicReference{
				Service: dynamicReferenceServiceSecretsManager, Name: "MySecret",
				SecretString: "SecretString", JSONKey: "password",
				VersionID: "01234567-89ab-cdef-0123-456789abcdef",
			},
		},
		"empty secret-string segment means SecretString": {
			input: "{{resolve:secretsmanager:MySecret::password}}",
			want: parsedDynamicReference{
				Service: dynamicReferenceServiceSecretsManager, Name: "MySecret", JSONKey: "password",
			},
		},
		"doc example: cross-account ARN, whole secret": {
			input: "{{resolve:secretsmanager:arn:aws:secretsmanager:us-west-2:123456789012:secret:MySecret}}",
			want: parsedDynamicReference{
				Service: dynamicReferenceServiceSecretsManager,
				Name:    "arn:aws:secretsmanager:us-west-2:123456789012:secret:MySecret",
			},
		},
		"doc example: cross-account ARN with json key": {
			input: "{{resolve:secretsmanager:arn:aws:secretsmanager:us-west-2:123456789012:secret:MySecret:SecretString:password}}",
			want: parsedDynamicReference{
				Service:      dynamicReferenceServiceSecretsManager,
				Name:         "arn:aws:secretsmanager:us-west-2:123456789012:secret:MySecret",
				SecretString: "SecretString",
				JSONKey:      "password",
			},
		},
		"ARN with the six-character random suffix Secrets Manager appends": {
			input: "{{resolve:secretsmanager:arn:aws:secretsmanager:eu-west-1:123456789012:secret:prod/db-AbCdEf:SecretString:username}}",
			want: parsedDynamicReference{
				Service:      dynamicReferenceServiceSecretsManager,
				Name:         "arn:aws:secretsmanager:eu-west-1:123456789012:secret:prod/db-AbCdEf",
				SecretString: "SecretString",
				JSONKey:      "username",
			},
		},
		"ARN in a non-commercial partition": {
			input: "{{resolve:secretsmanager:arn:aws-us-gov:secretsmanager:us-gov-west-1:123456789012:secret:MySecret-a1b2c3}}",
			want: parsedDynamicReference{
				Service: dynamicReferenceServiceSecretsManager,
				Name:    "arn:aws-us-gov:secretsmanager:us-gov-west-1:123456789012:secret:MySecret-a1b2c3",
			},
		},
		"truncated ARN is rejected rather than silently read as a name": {
			input:         "{{resolve:secretsmanager:arn:aws:secretsmanager:us-west-2:123456789012:secret}}",
			expectError:   true,
			errorContains: "not a complete Secrets Manager secret ARN",
		},
		"secret name is empty": {
			input:         "{{resolve:secretsmanager:}}",
			expectError:   true,
			errorContains: "names no secret",
		},
		"too many segments after the secret id": {
			input:         "{{resolve:secretsmanager:MySecret:SecretString:key:AWSCURRENT:v1:extra}}",
			expectError:   true,
			errorContains: "at most four",
		},
		"secret-string segment must be SecretString": {
			input:         "{{resolve:secretsmanager:MySecret:SecretBinary:key}}",
			expectError:   true,
			errorContains: "only supported value",
		},
		"version-stage and version-id are mutually exclusive": {
			input:         "{{resolve:secretsmanager:MySecret:SecretString:key:AWSCURRENT:v1}}",
			expectError:   true,
			errorContains: "don't specify the other",
		},

		// --- shape errors --------------------------------------------------
		"empty string": {
			input:         "",
			expectError:   true,
			errorContains: "empty string",
		},
		"plain string is not a reference": {
			input:         "/aws/service/ami",
			expectError:   true,
			errorContains: "is not a CloudFormation dynamic reference",
		},
		"reference embedded in surrounding text is the backend's job": {
			input:         "prefix-{{resolve:ssm:/a/b}}-suffix",
			expectError:   true,
			errorContains: "synthesis backend's job",
		},
		"leading text only": {
			input:         "ami={{resolve:ssm:/a/b}}",
			expectError:   true,
			errorContains: "synthesis backend's job",
		},
		"two references in one string": {
			input:         "{{resolve:ssm:/a}}{{resolve:ssm:/b}}",
			expectError:   true,
			errorContains: "more than one dynamic reference",
		},
		"reference ending in a backslash is unresolvable in CloudFormation": {
			input:         `{{resolve:ssm:/a/b\}}`,
			expectError:   true,
			errorContains: "ends with a backslash",
		},
		"unknown service": {
			input:         "{{resolve:s3:my-bucket}}",
			expectError:   true,
			errorContains: "not a CloudFormation dynamic reference service",
		},
		"no service segment at all": {
			input:         "{{resolve:}}",
			expectError:   true,
			errorContains: "names no service",
		},
		"whitespace around the reference is not trimmed away": {
			input:         " {{resolve:ssm:/a/b}} ",
			expectError:   true,
			errorContains: "is not a CloudFormation dynamic reference",
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := parseDynamicReference(testCase.input)

			if testCase.expectError {
				if err == nil {
					t.Fatalf("expected an error, got %+v", got)
				}
				if !strings.Contains(err.Error(), testCase.errorContains) {
					t.Fatalf("expected error to contain %q, got %q", testCase.errorContains, err.Error())
				}
				if isDynamicReference(testCase.input) {
					t.Errorf("is_dynamic_reference(%q) = true, want false", testCase.input)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			if got != testCase.want {
				t.Fatalf("parsed %+v, want %+v", got, testCase.want)
			}
			if !isDynamicReference(testCase.input) {
				t.Errorf("is_dynamic_reference(%q) = false, want true", testCase.input)
			}
		})
	}
}
