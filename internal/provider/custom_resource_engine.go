// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mathrand "math/rand"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/smithy-go"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// crDefaultPollInterval is the interval at which the engine polls the S3
// response object while waiting for a custom resource handler to respond,
// used when customResourceEngine.pollInterval is unset (zero).
const crDefaultPollInterval = 2 * time.Second

// crResponseMaxBytes is CloudFormation's documented response size limit for
// custom resources.
const crResponseMaxBytes = 4096

// crPresignExpiryBuffer is added on top of the resource's service_timeout
// when computing how long the pre-signed response PUT URL stays valid, so
// the URL does not expire moments before a handler that responds right at
// the timeout boundary would use it.
const crPresignExpiryBuffer = 5 * time.Minute

// crLambdaInvoker is the subset of the Lambda API client used to dispatch
// custom resource events. Implemented by *lambda.Client; mocked in tests.
type crLambdaInvoker interface {
	Invoke(ctx context.Context, params *lambda.InvokeInput, optFns ...func(*lambda.Options)) (*lambda.InvokeOutput, error)
}

// crSNSPublisher is the subset of the SNS API client used to dispatch custom
// resource events. Implemented by *sns.Client; mocked in tests.
type crSNSPublisher interface {
	Publish(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error)
}

// crResponseStore wraps S3 presigned-PUT generation and response object
// retrieval/cleanup, so the engine's polling logic can be unit tested
// without AWS.
type crResponseStore interface {
	// PresignPut returns a pre-signed URL a custom resource handler can PUT
	// its JSON response to.
	PresignPut(ctx context.Context, bucket, key string, expires time.Duration) (string, error)
	// Get retrieves the response object. found is false when the object does
	// not exist yet (the handler has not responded), which is not an error.
	Get(ctx context.Context, bucket, key string) ([]byte, bool, error)
	// Delete removes the response object. Callers treat a Delete failure as
	// a best-effort warning, not an error.
	Delete(ctx context.Context, bucket, key string) error
}

// crEvent is the CloudFormation custom resource request event, delivered as
// the JSON payload to the service token. Field names and casing exactly
// match what CloudFormation sends.
type crEvent struct {
	RequestType           string         `json:"RequestType"`
	ResponseURL           string         `json:"ResponseURL"`
	StackId               string         `json:"StackId"`
	RequestId             string         `json:"RequestId"`
	ResourceType          string         `json:"ResourceType"`
	LogicalResourceId     string         `json:"LogicalResourceId"`
	PhysicalResourceId    string         `json:"PhysicalResourceId,omitempty"`
	ResourceProperties    map[string]any `json:"ResourceProperties"`
	OldResourceProperties map[string]any `json:"OldResourceProperties,omitempty"`
}

// crResponse is the CloudFormation custom resource response object, PUT by
// the handler to the pre-signed ResponseURL. Field names and casing exactly
// match what CloudFormation expects.
type crResponse struct {
	Status             string         `json:"Status"`
	Reason             string         `json:"Reason,omitempty"`
	PhysicalResourceId string         `json:"PhysicalResourceId,omitempty"`
	StackId            string         `json:"StackId"`
	RequestId          string         `json:"RequestId"`
	LogicalResourceId  string         `json:"LogicalResourceId"`
	NoEcho             bool           `json:"NoEcho,omitempty"`
	Data               map[string]any `json:"Data,omitempty"`
}

// crInvocation is a single CloudFormation custom resource protocol exchange:
// send one RequestType event to service_token, then wait for exactly one
// response object.
type crInvocation struct {
	RequestType           string
	ServiceToken          string
	StackId               string
	RequestId             string
	ResourceType          string
	LogicalResourceId     string
	PhysicalResourceId    string // empty on Create; the (prior) physical id on Update/Delete
	ResourceProperties    map[string]any
	OldResourceProperties map[string]any // only meaningful when RequestType == "Update"
	// OldServiceToken is the ServiceToken merged into OldResourceProperties,
	// only meaningful when RequestType == "Update". CloudFormation's
	// OldResourceProperties reflects the prior template's properties
	// verbatim, including whatever ServiceToken was in effect at that time
	// -- which may differ from ServiceToken above if the update also changed
	// the service token. Falls back to ServiceToken when empty.
	OldServiceToken   string
	ServiceTimeout    time.Duration
	ResponseBucket    string
	ResponseKeyPrefix string
}

// crResult is the outcome of a successful crInvocation.
type crResult struct {
	PhysicalResourceId string
	Data               map[string]any
	NoEcho             bool
}

// customResourceEngine faithfully emulates the CloudFormation ENGINE side of
// the custom resource protocol: dispatching a request event to a Lambda or
// SNS service token, then polling an S3 response object until the handler
// responds or service_timeout elapses.
type customResourceEngine struct {
	lambdaClient crLambdaInvoker
	snsClient    crSNSPublisher
	store        crResponseStore

	// pollInterval is the base interval between S3 GetObject polls while
	// waiting for a response. Zero means crDefaultPollInterval; tests set
	// this to a tiny duration to run fast.
	pollInterval time.Duration
}

// Invoke sends one CloudFormation custom resource protocol event (Create,
// Update, or Delete) to inv.ServiceToken and waits for the handler's
// response, returning the resulting physical resource id, response Data, and
// NoEcho flag. It does not implement any of the higher-level
// create/update/delete state semantics (e.g. replacement cleanup deletes,
// PhysicalResourceId change detection) -- that composition lives in the
// resource implementation, which may call Invoke more than once per
// Terraform operation.
func (e *customResourceEngine) Invoke(ctx context.Context, inv crInvocation) (crResult, error) {
	if inv.ResponseBucket == "" {
		return crResult{}, errors.New(
			"no S3 bucket configured for custom resource response transport: " +
				"set response_bucket on this cfncompat_custom_resource or custom_resource_bucket on the provider",
		)
	}

	key := inv.ResponseKeyPrefix + "cfncompat/" + inv.RequestId + ".json"

	presignExpiry := inv.ServiceTimeout + crPresignExpiryBuffer
	responseURL, err := e.store.PresignPut(ctx, inv.ResponseBucket, key, presignExpiry)
	if err != nil {
		return crResult{}, fmt.Errorf("presigning custom resource response URL: %w", err)
	}

	// CloudFormation includes ServiceToken inside ResourceProperties (and,
	// for an Update event, inside OldResourceProperties too).
	resourceProperties := crMergeServiceToken(inv.ResourceProperties, inv.ServiceToken)

	var oldResourceProperties map[string]any
	if inv.RequestType == "Update" {
		oldServiceToken := inv.OldServiceToken
		if oldServiceToken == "" {
			oldServiceToken = inv.ServiceToken
		}
		oldResourceProperties = crMergeServiceToken(inv.OldResourceProperties, oldServiceToken)
	}

	event := crEvent{
		RequestType:           inv.RequestType,
		ResponseURL:           responseURL,
		StackId:               inv.StackId,
		RequestId:             inv.RequestId,
		ResourceType:          inv.ResourceType,
		LogicalResourceId:     inv.LogicalResourceId,
		PhysicalResourceId:    inv.PhysicalResourceId,
		ResourceProperties:    resourceProperties,
		OldResourceProperties: oldResourceProperties,
	}

	if err := e.dispatch(ctx, inv.ServiceToken, event); err != nil {
		return crResult{}, fmt.Errorf("invoking service_token %q: %w", inv.ServiceToken, err)
	}

	resp, err := e.waitForResponse(ctx, inv.ResponseBucket, key, inv.ServiceTimeout)
	if err != nil {
		return crResult{}, err
	}

	if resp.Status != "SUCCESS" {
		reason := resp.Reason
		if reason == "" {
			reason = fmt.Sprintf("custom resource handler reported Status=%q with no Reason", resp.Status)
		}
		return crResult{}, errors.New(reason)
	}

	physicalResourceID := resp.PhysicalResourceId
	if physicalResourceID == "" {
		// Matches the CDK custom resource framework's defaulting behavior:
		// Create defaults to the (engine-generated) RequestId; Update/Delete
		// default to the prior physical resource id.
		if inv.RequestType == "Create" {
			physicalResourceID = inv.RequestId
		} else {
			physicalResourceID = inv.PhysicalResourceId
		}
	}

	return crResult{
		PhysicalResourceId: physicalResourceID,
		Data:               resp.Data,
		NoEcho:             resp.NoEcho,
	}, nil
}

// dispatch sends the marshaled event to serviceToken, dispatching to Lambda
// Invoke or SNS Publish based on the ARN's service segment.
func (e *customResourceEngine) dispatch(ctx context.Context, serviceToken string, event crEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling custom resource event: %w", err)
	}

	parsed, err := arn.Parse(serviceToken)
	if err != nil {
		return fmt.Errorf("service_token %q is not a valid ARN: %w", serviceToken, err)
	}

	switch parsed.Service {
	case "lambda":
		_, err := e.lambdaClient.Invoke(ctx, &lambda.InvokeInput{
			FunctionName:   aws.String(serviceToken),
			InvocationType: lambdatypes.InvocationTypeEvent,
			Payload:        payload,
		})
		return err
	case "sns":
		_, err := e.snsClient.Publish(ctx, &sns.PublishInput{
			TopicArn: aws.String(serviceToken),
			Message:  aws.String(string(payload)),
		})
		return err
	default:
		return fmt.Errorf("service_token %q has unsupported ARN service %q: must be a lambda or sns ARN", serviceToken, parsed.Service)
	}
}

// waitForResponse polls the S3 response object every pollInterval (plus a
// little jitter) until it appears or timeout elapses.
func (e *customResourceEngine) waitForResponse(ctx context.Context, bucket, key string, timeout time.Duration) (crResponse, error) {
	deadline := time.Now().Add(timeout)

	for {
		body, found, err := e.store.Get(ctx, bucket, key)
		if err != nil {
			return crResponse{}, fmt.Errorf("reading custom resource response object: %w", err)
		}

		if found {
			return e.parseResponse(ctx, bucket, key, body)
		}

		if !time.Now().Before(deadline) {
			return crResponse{}, fmt.Errorf(
				"custom resource did not respond within the ServiceTimeout of %ds",
				int(timeout.Seconds()),
			)
		}

		select {
		case <-ctx.Done():
			return crResponse{}, ctx.Err()
		case <-time.After(e.pollDelay()):
		}
	}
}

// parseResponse validates the response size, decodes it, and best-effort
// deletes the response object.
func (e *customResourceEngine) parseResponse(ctx context.Context, bucket, key string, body []byte) (crResponse, error) {
	if len(body) > crResponseMaxBytes {
		return crResponse{}, fmt.Errorf(
			"custom resource response exceeds CloudFormation's %d byte response size limit (got %d bytes)",
			crResponseMaxBytes, len(body),
		)
	}

	var resp crResponse
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&resp); err != nil {
		return crResponse{}, fmt.Errorf("decoding custom resource response: %w", err)
	}

	if err := e.store.Delete(ctx, bucket, key); err != nil {
		tflog.Warn(ctx, "cfncompat_custom_resource: failed to delete response object", map[string]any{
			"bucket": bucket,
			"key":    key,
			"error":  err.Error(),
		})
	}

	return resp, nil
}

// pollDelay returns the base poll interval plus a small amount of jitter (up
// to ~20%), so many concurrent custom resources don't all hammer S3 in
// lockstep.
func (e *customResourceEngine) pollDelay() time.Duration {
	base := e.pollInterval
	if base <= 0 {
		base = crDefaultPollInterval
	}

	jitterMax := int64(base) / 5
	if jitterMax <= 0 {
		return base
	}

	return base + time.Duration(mathrand.Int63n(jitterMax)) //nolint:gosec // non-cryptographic jitter, not a security boundary
}

// crMergeServiceToken returns a copy of props with "ServiceToken" set to
// serviceToken, matching CloudFormation's behavior of merging ServiceToken
// into the properties map delivered to the handler. props may be nil.
func crMergeServiceToken(props map[string]any, serviceToken string) map[string]any {
	merged := make(map[string]any, len(props)+1)
	for k, v := range props {
		merged[k] = v
	}
	merged["ServiceToken"] = serviceToken
	return merged
}

// newCustomResourceRequestID generates a random UUID (v4-shaped) RequestId
// using crypto/rand, avoiding an extra UUID library dependency.
func newCustomResourceRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating RequestId: %w", err)
	}

	// Set version (4) and variant (RFC 4122) bits so the result looks like a
	// standard random UUID, even though nothing in this protocol requires
	// strict UUID compliance.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// s3ResponseStore is the real, AWS-backed crResponseStore implementation.
type s3ResponseStore struct {
	client        *s3.Client
	presignClient *s3.PresignClient
}

// newS3ResponseStore wraps an S3 client into a crResponseStore.
func newS3ResponseStore(client *s3.Client) *s3ResponseStore {
	return &s3ResponseStore{
		client:        client,
		presignClient: s3.NewPresignClient(client),
	}
}

// PresignPut generates a pre-signed PUT URL. It deliberately does not set
// ContentType on the request, so the presigned URL does not constrain the
// Content-Type header the handler PUTs with (custom resource handlers,
// including CDK's, PUT with an empty Content-Type).
func (s *s3ResponseStore) PresignPut(ctx context.Context, bucket, key string, expires time.Duration) (string, error) {
	req, err := s.presignClient.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, func(o *s3.PresignOptions) {
		o.Expires = expires
	})
	if err != nil {
		return "", err
	}

	return req.URL, nil
}

// Get retrieves the response object, returning found=false (with no error)
// when the object does not exist yet.
func (s *s3ResponseStore) Get(ctx context.Context, bucket, key string) ([]byte, bool, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var noSuchKey *s3types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return nil, false, nil
		}

		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "NoSuchKey", "NotFound":
				return nil, false, nil
			}
		}

		return nil, false, err
	}
	defer out.Body.Close()

	body, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, false, fmt.Errorf("reading response object body: %w", err)
	}

	return body, true, nil
}

// Delete removes the response object.
func (s *s3ResponseStore) Delete(ctx context.Context, bucket, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	return err
}
