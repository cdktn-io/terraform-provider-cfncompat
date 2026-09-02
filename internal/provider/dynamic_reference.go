// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"regexp"
	"strings"
)

// The CloudFormation dynamic-reference grammar, shared by
// provider::cfncompat::parse_dynamic_reference and
// provider::cfncompat::is_dynamic_reference.
//
// A dynamic reference is `{{resolve:service:...}}`, where the tail is
// service-specific:
//
//	{{resolve:ssm:parameter-name:version}}
//	{{resolve:ssm-secure:parameter-name:version}}
//	{{resolve:secretsmanager:secret-id:secret-string:json-key:version-stage:version-id}}
//
// See:
//   - https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references.html
//   - https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references-ssm.html
//   - https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references-ssm-secure-strings.html
//   - https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references-secretsmanager.html

// Dynamic-reference service names.
const (
	dynamicReferenceServiceSSM            = "ssm"
	dynamicReferenceServiceSSMSecure      = "ssm-secure"
	dynamicReferenceServiceSecretsManager = "secretsmanager"
)

// dynamicReferenceOpen and dynamicReferenceClose delimit a reference.
const (
	dynamicReferenceOpen  = "{{resolve:"
	dynamicReferenceClose = "}}"
)

// secretStringSegment is the only legal value of the secretsmanager
// `secret-string` segment.
const secretStringSegment = "SecretString"

// ssmParameterNamePattern is CloudFormation's own documented pattern for the
// parameter-name segment of an ssm/ssm-secure reference.
var ssmParameterNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.\-/]+$`)

// ssmVersionPattern is the version segment: a run of digits.
var ssmVersionPattern = regexp.MustCompile(`^\d+$`)

// secretARNPartCount is the number of colon-separated parts of a Secrets
// Manager secret ARN:
//
//	arn : aws : secretsmanager : us-west-2 : 123456789012 : secret : MySecret-a1b2c3
//
// The random 6-character suffix Secrets Manager appends to a secret's name is
// joined with a hyphen, not a colon, so a full secret ARN is always exactly
// seven parts. This is what makes a secret-id ARN unambiguous inside a
// colon-delimited reference: consume seven parts, and every remaining part is
// a positional segment.
const secretARNPartCount = 7

// parsedDynamicReference is the fixed attribute set
// provider::cfncompat::parse_dynamic_reference returns. Segments that the
// reference does not carry are empty, and are rendered as null.
type parsedDynamicReference struct {
	Service string
	Name    string

	// Version is the ssm/ssm-secure version segment, kept as a string so the
	// object's attribute types never change shape and an absent version is
	// null rather than a sentinel number.
	Version string

	// SecretString is the secretsmanager `secret-string` segment: the literal
	// "SecretString" when the reference spells it out, empty otherwise.
	SecretString string
	JSONKey      string
	VersionStage string
	VersionID    string
}

// isDynamicReference reports whether s is exactly one well-formed dynamic
// reference.
func isDynamicReference(s string) bool {
	_, err := parseDynamicReference(s)
	return err == nil
}

// parseDynamicReference parses exactly one whole `{{resolve:...}}` string.
//
// It deliberately rejects a string that merely *contains* a reference:
// expanding a reference embedded in surrounding text means emitting one data
// source per reference and rebuilding the string with interpolation, which is
// the synthesis backend's job (RFC 006's ownership split, restated in
// RFC 007 §5).
func parseDynamicReference(s string) (parsedDynamicReference, error) {
	var out parsedDynamicReference

	if s == "" {
		return out, fmt.Errorf("expected a CloudFormation dynamic reference, got an empty string")
	}
	if !strings.HasPrefix(s, dynamicReferenceOpen) || !strings.HasSuffix(s, dynamicReferenceClose) {
		return out, fmt.Errorf(
			"%q is not a CloudFormation dynamic reference: the whole string must be one reference, "+
				"starting with %q and ending with %q. A reference embedded in surrounding text is not "+
				"accepted -- splitting a string around its references and rebuilding it with interpolation "+
				"is the synthesis backend's job, not the provider's",
			s, dynamicReferenceOpen, dynamicReferenceClose)
	}

	body := s[len(dynamicReferenceOpen) : len(s)-len(dynamicReferenceClose)]

	if strings.Contains(body, "{{") || strings.Contains(body, "}}") {
		return out, fmt.Errorf(
			"%q contains more than one dynamic reference. parse_dynamic_reference takes exactly one; "+
				"emit one data source per reference and rebuild the surrounding string with interpolation", s)
	}
	if strings.HasSuffix(body, `\`) {
		// CloudFormation: "Don't create a dynamic reference that ends with a
		// backslash. CloudFormation can't resolve these references."
		return out, fmt.Errorf(
			"the dynamic reference %q ends with a backslash, which CloudFormation cannot resolve", s)
	}

	service, rest, found := strings.Cut(body, ":")
	if !found || service == "" {
		return out, fmt.Errorf(
			"the dynamic reference %q names no service: the form is {{resolve:<service>:...}} with a "+
				"service of %q, %q or %q",
			s, dynamicReferenceServiceSSM, dynamicReferenceServiceSSMSecure, dynamicReferenceServiceSecretsManager)
	}
	out.Service = service

	switch service {
	case dynamicReferenceServiceSSM, dynamicReferenceServiceSSMSecure:
		return parseSSMDynamicReference(s, service, rest)
	case dynamicReferenceServiceSecretsManager:
		return parseSecretsManagerDynamicReference(s, rest)
	default:
		return out, fmt.Errorf(
			"%q is not a CloudFormation dynamic reference service. CloudFormation defines exactly three: "+
				"%q, %q and %q",
			service, dynamicReferenceServiceSSM, dynamicReferenceServiceSSMSecure, dynamicReferenceServiceSecretsManager)
	}
}

// parseSSMDynamicReference parses the tail of an ssm/ssm-secure reference:
// `parameter-name` with an optional `:version`.
func parseSSMDynamicReference(whole, service, rest string) (parsedDynamicReference, error) {
	out := parsedDynamicReference{Service: service}

	name := rest
	version := ""
	if idx := strings.LastIndex(rest, ":"); idx >= 0 {
		name = rest[:idx]
		version = rest[idx+1:]
		if !ssmVersionPattern.MatchString(version) {
			return out, fmt.Errorf(
				"the dynamic reference %q has the trailing segment %q, which is not a version number. "+
					"CloudFormation's pattern for a %s reference is "+
					"{{resolve:%s:[a-zA-Z0-9_.\\-/]+(:\\d+)?}}: a parameter name, optionally followed by a "+
					"numeric version. Parameter labels are not supported in dynamic references",
				whole, version, service, service)
		}
	}

	if name == "" {
		return out, fmt.Errorf("the dynamic reference %q names no parameter", whole)
	}
	if !ssmParameterNamePattern.MatchString(name) {
		return out, fmt.Errorf(
			"the parameter name %q in %q contains characters CloudFormation does not allow in a %s "+
				"dynamic reference; the documented pattern is [a-zA-Z0-9_.\\-/]+",
			name, whole, service)
	}

	out.Name = name
	out.Version = version
	return out, nil
}

// parseSecretsManagerDynamicReference parses the tail of a secretsmanager
// reference: `secret-id` followed by up to four positional segments.
//
// The secret-id may itself be a full ARN, which contains colons. It is
// disambiguated positionally, exactly as CloudFormation's own examples imply:
// an id that starts with "arn:" consumes the ARN's seven colon-separated
// parts, and every part after that is a positional segment.
func parseSecretsManagerDynamicReference(whole, rest string) (parsedDynamicReference, error) {
	out := parsedDynamicReference{Service: dynamicReferenceServiceSecretsManager}

	parts := strings.Split(rest, ":")

	var segments []string
	if strings.HasPrefix(rest, "arn:") {
		if len(parts) < secretARNPartCount {
			return out, fmt.Errorf(
				"the secret id in %q starts with \"arn:\" but is not a complete Secrets Manager secret ARN. "+
					"A secret ARN has %d colon-separated parts "+
					"(arn:<partition>:secretsmanager:<region>:<account>:secret:<name>-<suffix>); "+
					"got %d", whole, secretARNPartCount, len(parts))
		}
		out.Name = strings.Join(parts[:secretARNPartCount], ":")
		segments = parts[secretARNPartCount:]
	} else {
		out.Name = parts[0]
		segments = parts[1:]
	}

	if out.Name == "" {
		return out, fmt.Errorf("the dynamic reference %q names no secret", whole)
	}
	if len(segments) > 4 {
		return out, fmt.Errorf(
			"the dynamic reference %q has %d segments after the secret id, and CloudFormation's pattern "+
				"has at most four "+
				"({{resolve:secretsmanager:secret-id:secret-string:json-key:version-stage:version-id}})",
			whole, len(segments))
	}

	// Positional, any of them possibly empty:
	// secret-string, json-key, version-stage, version-id.
	get := func(i int) string {
		if i < len(segments) {
			return segments[i]
		}
		return ""
	}
	out.SecretString = get(0)
	out.JSONKey = get(1)
	out.VersionStage = get(2)
	out.VersionID = get(3)

	if out.SecretString != "" && out.SecretString != secretStringSegment {
		return out, fmt.Errorf(
			"the secret-string segment of %q is %q; CloudFormation documents %q as its only supported value "+
				"(an empty segment means the same thing)", whole, out.SecretString, secretStringSegment)
	}
	if out.VersionStage != "" && out.VersionID != "" {
		return out, fmt.Errorf(
			"the dynamic reference %q sets both a version-stage (%q) and a version-id (%q); "+
				"CloudFormation documents that if you use one, you don't specify the other",
			whole, out.VersionStage, out.VersionID)
	}

	return out, nil
}
