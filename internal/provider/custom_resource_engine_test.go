// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

// fakeResponseStore is an in-memory crResponseStore for unit tests. Objects
// are keyed by "bucket/key". PresignPut encodes bucket/key into a fake URL
// (via query parameters) so a fake handler can recover them without the
// engine ever needing to parse ResponseURL itself (the real engine never
// does -- it polls S3 directly, matching the spec).
type fakeResponseStore struct {
	mu   sync.Mutex
	objs map[string][]byte

	presignErr error
	getErr     error
	deleteErr  error

	deleteCalls []string
}

func newFakeResponseStore() *fakeResponseStore {
	return &fakeResponseStore{objs: map[string][]byte{}}
}

func fakeStoreObjKey(bucket, key string) string { return bucket + "/" + key }

func (s *fakeResponseStore) PresignPut(_ context.Context, bucket, key string, _ time.Duration) (string, error) {
	if s.presignErr != nil {
		return "", s.presignErr
	}

	u := url.URL{
		Scheme:   "https",
		Host:     "fake-presigned.example.com",
		Path:     "/" + key,
		RawQuery: url.Values{"bucket": {bucket}, "key": {key}}.Encode(),
	}
	return u.String(), nil
}

func (s *fakeResponseStore) put(bucket, key string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objs[fakeStoreObjKey(bucket, key)] = body
}

func (s *fakeResponseStore) Get(_ context.Context, bucket, key string) ([]byte, bool, error) {
	if s.getErr != nil {
		return nil, false, s.getErr
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.objs[fakeStoreObjKey(bucket, key)]
	return body, ok, nil
}

func (s *fakeResponseStore) Delete(_ context.Context, bucket, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteCalls = append(s.deleteCalls, fakeStoreObjKey(bucket, key))
	delete(s.objs, fakeStoreObjKey(bucket, key))
	return s.deleteErr
}

// fakeResponseURLBucketKey recovers the bucket/key a fakeResponseStore
// encoded into a presigned URL, so a fake handler can write its response to
// the right place.
func fakeResponseURLBucketKey(t *testing.T, rawURL string) (bucket, key string) {
	t.Helper()

	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("failed to parse fake response URL %q: %s", rawURL, err)
	}
	q := u.Query()
	return q.Get("bucket"), q.Get("key")
}

// crHandlerFunc simulates a custom resource handler: given the request
// event, it returns the response the handler would PUT.
type crHandlerFunc func(event crEvent) crResponse

// fakeLambdaInvoker is a crLambdaInvoker fake that decodes the invoke
// payload as a crEvent, records the call, and (if handler is non-nil)
// synchronously writes the handler's response into store.
type fakeLambdaInvoker struct {
	t         *testing.T
	store     *fakeResponseStore
	handler   crHandlerFunc
	invokeErr error

	mu    sync.Mutex
	calls []crEvent
}

func (f *fakeLambdaInvoker) Invoke(_ context.Context, in *lambda.InvokeInput, _ ...func(*lambda.Options)) (*lambda.InvokeOutput, error) {
	if f.invokeErr != nil {
		return nil, f.invokeErr
	}

	var event crEvent
	if err := json.Unmarshal(in.Payload, &event); err != nil {
		f.t.Fatalf("fakeLambdaInvoker: failed to decode invoke payload: %s", err)
	}

	f.mu.Lock()
	f.calls = append(f.calls, event)
	f.mu.Unlock()

	if aws.ToString(in.FunctionName) == "" {
		f.t.Fatalf("fakeLambdaInvoker: FunctionName was not set")
	}

	if f.handler != nil {
		resp := f.handler(event)
		body, err := json.Marshal(resp)
		if err != nil {
			f.t.Fatalf("fakeLambdaInvoker: failed to marshal handler response: %s", err)
		}
		bucket, key := fakeResponseURLBucketKey(f.t, event.ResponseURL)
		f.store.put(bucket, key, body)
	}

	return &lambda.InvokeOutput{}, nil
}

func (f *fakeLambdaInvoker) lastCall() crEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

// fakeSNSPublisher is a crSNSPublisher fake, mirroring fakeLambdaInvoker.
type fakeSNSPublisher struct {
	t          *testing.T
	store      *fakeResponseStore
	handler    crHandlerFunc
	publishErr error

	mu    sync.Mutex
	calls []crEvent
}

func (f *fakeSNSPublisher) Publish(_ context.Context, in *sns.PublishInput, _ ...func(*sns.Options)) (*sns.PublishOutput, error) {
	if f.publishErr != nil {
		return nil, f.publishErr
	}

	var event crEvent
	if err := json.Unmarshal([]byte(aws.ToString(in.Message)), &event); err != nil {
		f.t.Fatalf("fakeSNSPublisher: failed to decode publish message: %s", err)
	}

	f.mu.Lock()
	f.calls = append(f.calls, event)
	f.mu.Unlock()

	if aws.ToString(in.TopicArn) == "" {
		f.t.Fatalf("fakeSNSPublisher: TopicArn was not set")
	}

	if f.handler != nil {
		resp := f.handler(event)
		body, err := json.Marshal(resp)
		if err != nil {
			f.t.Fatalf("fakeSNSPublisher: failed to marshal handler response: %s", err)
		}
		bucket, key := fakeResponseURLBucketKey(f.t, event.ResponseURL)
		f.store.put(bucket, key, body)
	}

	return &sns.PublishOutput{}, nil
}

// newTestEngine builds a customResourceEngine wired to fresh fakes, with a
// fast poll interval suitable for unit tests.
func newTestEngine(t *testing.T, handler crHandlerFunc) (*customResourceEngine, *fakeLambdaInvoker, *fakeSNSPublisher, *fakeResponseStore) {
	t.Helper()

	store := newFakeResponseStore()
	lambdaInvoker := &fakeLambdaInvoker{t: t, store: store, handler: handler}
	snsPublisher := &fakeSNSPublisher{t: t, store: store, handler: handler}

	engine := &customResourceEngine{
		lambdaClient: lambdaInvoker,
		snsClient:    snsPublisher,
		store:        store,
		pollInterval: time.Millisecond,
	}

	return engine, lambdaInvoker, snsPublisher, store
}

const (
	testLambdaServiceToken = "arn:aws:lambda:us-east-1:123456789012:function:my-handler"
	testSNSServiceToken    = "arn:aws:sns:us-east-1:123456789012:my-topic"
)

func baseInvocation(requestType string) crInvocation {
	return crInvocation{
		RequestType:        requestType,
		ServiceToken:       testLambdaServiceToken,
		StackId:            "cfncompat/no-stack-id",
		RequestId:          "test-request-id",
		ResourceType:       "AWS::CloudFormation::CustomResource",
		LogicalResourceId:  "TestResource",
		ResourceProperties: map[string]any{"Foo": "bar"},
		ServiceTimeout:     time.Second,
		ResponseBucket:     "test-bucket",
	}
}

func successResponse(event crEvent) crResponse {
	return crResponse{
		Status:             "SUCCESS",
		PhysicalResourceId: event.PhysicalResourceId,
		StackId:            event.StackId,
		RequestId:          event.RequestId,
		LogicalResourceId:  event.LogicalResourceId,
	}
}

func TestCustomResourceEngineInvoke_CreateSuccess(t *testing.T) {
	t.Parallel()

	t.Run("defaults PhysicalResourceId to RequestId when response omits it", func(t *testing.T) {
		t.Parallel()

		engine, invoker, _, _ := newTestEngine(t, func(event crEvent) crResponse {
			return crResponse{Status: "SUCCESS"} // no PhysicalResourceId
		})

		inv := baseInvocation("Create")
		result, err := engine.Invoke(context.Background(), inv)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		if result.PhysicalResourceId != inv.RequestId {
			t.Errorf("PhysicalResourceId = %q, want %q (the RequestId)", result.PhysicalResourceId, inv.RequestId)
		}

		event := invoker.lastCall()
		if event.PhysicalResourceId != "" {
			t.Errorf("Create event PhysicalResourceId = %q, want empty", event.PhysicalResourceId)
		}
	})

	t.Run("uses response PhysicalResourceId when present", func(t *testing.T) {
		t.Parallel()

		engine, _, _, _ := newTestEngine(t, func(event crEvent) crResponse {
			return crResponse{Status: "SUCCESS", PhysicalResourceId: "handler-chosen-id"}
		})

		result, err := engine.Invoke(context.Background(), baseInvocation("Create"))
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		if result.PhysicalResourceId != "handler-chosen-id" {
			t.Errorf("PhysicalResourceId = %q, want handler-chosen-id", result.PhysicalResourceId)
		}
	})

	t.Run("returns Data when present", func(t *testing.T) {
		t.Parallel()

		engine, _, _, _ := newTestEngine(t, func(event crEvent) crResponse {
			resp := successResponse(event)
			resp.PhysicalResourceId = "id-1"
			resp.Data = map[string]any{"Key": "Value", "Count": json.Number("3")}
			return resp
		})

		result, err := engine.Invoke(context.Background(), baseInvocation("Create"))
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		if result.Data["Key"] != "Value" {
			t.Errorf("Data[Key] = %v, want Value", result.Data["Key"])
		}
	})

	t.Run("surfaces NoEcho", func(t *testing.T) {
		t.Parallel()

		engine, _, _, _ := newTestEngine(t, func(event crEvent) crResponse {
			resp := successResponse(event)
			resp.PhysicalResourceId = "id-1"
			resp.NoEcho = true
			return resp
		})

		result, err := engine.Invoke(context.Background(), baseInvocation("Create"))
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		if !result.NoEcho {
			t.Error("NoEcho = false, want true")
		}
	})
}

func TestCustomResourceEngineInvoke_CreateFailed(t *testing.T) {
	t.Parallel()

	engine, _, _, store := newTestEngine(t, func(event crEvent) crResponse {
		return crResponse{Status: "FAILED", Reason: "handler exploded"}
	})

	_, err := engine.Invoke(context.Background(), baseInvocation("Create"))
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "handler exploded") {
		t.Errorf("error = %q, want it to contain the Reason", err)
	}
	if len(store.deleteCalls) != 1 {
		t.Errorf("expected the response object to still be cleaned up, deleteCalls = %v", store.deleteCalls)
	}
}

func TestCustomResourceEngineInvoke_UpdateSameId(t *testing.T) {
	t.Parallel()

	engine, _, _, _ := newTestEngine(t, func(event crEvent) crResponse {
		return successResponse(event) // echoes back the same PhysicalResourceId
	})

	inv := baseInvocation("Update")
	inv.PhysicalResourceId = "existing-id"
	inv.OldResourceProperties = map[string]any{"Foo": "old-bar"}

	result, err := engine.Invoke(context.Background(), inv)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if result.PhysicalResourceId != "existing-id" {
		t.Errorf("PhysicalResourceId = %q, want existing-id (unchanged)", result.PhysicalResourceId)
	}
}

func TestCustomResourceEngineInvoke_UpdateDefaultsToPriorPhysicalResourceId(t *testing.T) {
	t.Parallel()

	engine, _, _, _ := newTestEngine(t, func(event crEvent) crResponse {
		return crResponse{Status: "SUCCESS"} // omits PhysicalResourceId
	})

	inv := baseInvocation("Update")
	inv.PhysicalResourceId = "existing-id"

	result, err := engine.Invoke(context.Background(), inv)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if result.PhysicalResourceId != "existing-id" {
		t.Errorf("PhysicalResourceId = %q, want existing-id (fallback to prior)", result.PhysicalResourceId)
	}
}

func TestCustomResourceEngineInvoke_UpdateNewId(t *testing.T) {
	t.Parallel()

	engine, _, _, _ := newTestEngine(t, func(event crEvent) crResponse {
		resp := successResponse(event)
		resp.PhysicalResourceId = "new-id"
		return resp
	})

	inv := baseInvocation("Update")
	inv.PhysicalResourceId = "existing-id"

	result, err := engine.Invoke(context.Background(), inv)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if result.PhysicalResourceId != "new-id" {
		t.Errorf("PhysicalResourceId = %q, want new-id", result.PhysicalResourceId)
	}
}

func TestCustomResourceEngineInvoke_UpdateFailed(t *testing.T) {
	t.Parallel()

	engine, _, _, _ := newTestEngine(t, func(event crEvent) crResponse {
		return crResponse{Status: "FAILED", Reason: "update exploded"}
	})

	inv := baseInvocation("Update")
	inv.PhysicalResourceId = "existing-id"

	_, err := engine.Invoke(context.Background(), inv)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "update exploded") {
		t.Errorf("error = %q, want it to contain the Reason", err)
	}
}

func TestCustomResourceEngineInvoke_DeleteSuccess(t *testing.T) {
	t.Parallel()

	engine, _, _, _ := newTestEngine(t, func(event crEvent) crResponse {
		return successResponse(event)
	})

	inv := baseInvocation("Delete")
	inv.PhysicalResourceId = "existing-id"

	result, err := engine.Invoke(context.Background(), inv)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if result.PhysicalResourceId != "existing-id" {
		t.Errorf("PhysicalResourceId = %q, want existing-id", result.PhysicalResourceId)
	}
}

func TestCustomResourceEngineInvoke_DeleteFailed(t *testing.T) {
	t.Parallel()

	engine, _, _, _ := newTestEngine(t, func(event crEvent) crResponse {
		return crResponse{Status: "FAILED", Reason: "delete exploded"}
	})

	inv := baseInvocation("Delete")
	inv.PhysicalResourceId = "existing-id"

	_, err := engine.Invoke(context.Background(), inv)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "delete exploded") {
		t.Errorf("error = %q, want it to contain the Reason", err)
	}
}

func TestCustomResourceEngineInvoke_Timeout(t *testing.T) {
	t.Parallel()

	// No handler: the fake store never receives a response object.
	engine, _, _, _ := newTestEngine(t, nil)

	inv := baseInvocation("Create")
	inv.ServiceTimeout = 5 * time.Millisecond

	_, err := engine.Invoke(context.Background(), inv)
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "did not respond within the ServiceTimeout") {
		t.Errorf("error = %q, want a ServiceTimeout message", err)
	}
}

func TestCustomResourceEngineInvoke_ResponseTooLarge(t *testing.T) {
	t.Parallel()

	hugeValue := strings.Repeat("x", 5000)
	engine, _, _, _ := newTestEngine(t, func(event crEvent) crResponse {
		resp := successResponse(event)
		resp.PhysicalResourceId = "id-1"
		resp.Data = map[string]any{"Huge": hugeValue}
		return resp
	})

	_, err := engine.Invoke(context.Background(), baseInvocation("Create"))
	if err == nil {
		t.Fatal("expected an error for an oversized response, got nil")
	}
	if !strings.Contains(err.Error(), "4096 byte") {
		t.Errorf("error = %q, want it to mention the 4096 byte limit", err)
	}
}

func TestCustomResourceEngineInvoke_SNSDispatch(t *testing.T) {
	t.Parallel()

	engine, lambdaInvoker, snsPublisher, _ := newTestEngine(t, func(event crEvent) crResponse {
		resp := successResponse(event)
		resp.PhysicalResourceId = "id-1"
		return resp
	})

	inv := baseInvocation("Create")
	inv.ServiceToken = testSNSServiceToken

	_, err := engine.Invoke(context.Background(), inv)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if len(snsPublisher.calls) != 1 {
		t.Errorf("expected exactly one SNS Publish call, got %d", len(snsPublisher.calls))
	}
	if len(lambdaInvoker.calls) != 0 {
		t.Errorf("expected zero Lambda Invoke calls, got %d", len(lambdaInvoker.calls))
	}
}

func TestCustomResourceEngineInvoke_UnsupportedServiceTokenARN(t *testing.T) {
	t.Parallel()

	engine, _, _, _ := newTestEngine(t, nil)

	inv := baseInvocation("Create")
	inv.ServiceToken = "arn:aws:sqs:us-east-1:123456789012:my-queue"

	_, err := engine.Invoke(context.Background(), inv)
	if err == nil {
		t.Fatal("expected an error for an unsupported ARN service, got nil")
	}
	if !strings.Contains(err.Error(), "sqs") {
		t.Errorf("error = %q, want it to mention the unsupported service", err)
	}
}

func TestCustomResourceEngineInvoke_InvalidServiceTokenARN(t *testing.T) {
	t.Parallel()

	engine, _, _, _ := newTestEngine(t, nil)

	inv := baseInvocation("Create")
	inv.ServiceToken = "not-an-arn"

	_, err := engine.Invoke(context.Background(), inv)
	if err == nil {
		t.Fatal("expected an error for an invalid ARN, got nil")
	}
}

func TestCustomResourceEngineInvoke_MissingResponseBucket(t *testing.T) {
	t.Parallel()

	engine, _, _, _ := newTestEngine(t, nil)

	inv := baseInvocation("Create")
	inv.ResponseBucket = ""

	_, err := engine.Invoke(context.Background(), inv)
	if err == nil {
		t.Fatal("expected an error for a missing response bucket, got nil")
	}
	if !strings.Contains(err.Error(), "response_bucket") {
		t.Errorf("error = %q, want it to mention response_bucket", err)
	}
}

func TestCustomResourceEngineInvoke_ServiceTokenMergedIntoResourceProperties(t *testing.T) {
	t.Parallel()

	engine, invoker, _, _ := newTestEngine(t, func(event crEvent) crResponse {
		resp := successResponse(event)
		resp.PhysicalResourceId = "id-1"
		return resp
	})

	inv := baseInvocation("Create")

	_, err := engine.Invoke(context.Background(), inv)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	event := invoker.lastCall()
	if event.ResourceProperties["ServiceToken"] != inv.ServiceToken {
		t.Errorf("ResourceProperties[ServiceToken] = %v, want %q", event.ResourceProperties["ServiceToken"], inv.ServiceToken)
	}
	if event.ResourceProperties["Foo"] != "bar" {
		t.Errorf("ResourceProperties[Foo] = %v, want bar (user properties preserved)", event.ResourceProperties["Foo"])
	}
}

func TestCustomResourceEngineInvoke_OldResourcePropertiesOnlyOnUpdate(t *testing.T) {
	t.Parallel()

	t.Run("Create omits OldResourceProperties", func(t *testing.T) {
		t.Parallel()

		engine, invoker, _, _ := newTestEngine(t, func(event crEvent) crResponse {
			resp := successResponse(event)
			resp.PhysicalResourceId = "id-1"
			return resp
		})

		_, err := engine.Invoke(context.Background(), baseInvocation("Create"))
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		if invoker.lastCall().OldResourceProperties != nil {
			t.Errorf("Create event OldResourceProperties = %v, want nil", invoker.lastCall().OldResourceProperties)
		}
	})

	t.Run("Delete omits OldResourceProperties", func(t *testing.T) {
		t.Parallel()

		engine, invoker, _, _ := newTestEngine(t, func(event crEvent) crResponse {
			return successResponse(event)
		})

		inv := baseInvocation("Delete")
		inv.PhysicalResourceId = "existing-id"

		_, err := engine.Invoke(context.Background(), inv)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		if invoker.lastCall().OldResourceProperties != nil {
			t.Errorf("Delete event OldResourceProperties = %v, want nil", invoker.lastCall().OldResourceProperties)
		}
	})

	t.Run("Update includes OldResourceProperties with the merged ServiceToken", func(t *testing.T) {
		t.Parallel()

		engine, invoker, _, _ := newTestEngine(t, func(event crEvent) crResponse {
			return successResponse(event)
		})

		inv := baseInvocation("Update")
		inv.PhysicalResourceId = "existing-id"
		inv.OldResourceProperties = map[string]any{"Foo": "old-bar"}

		_, err := engine.Invoke(context.Background(), inv)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}

		event := invoker.lastCall()
		if event.OldResourceProperties == nil {
			t.Fatal("Update event OldResourceProperties = nil, want non-nil")
		}
		if event.OldResourceProperties["Foo"] != "old-bar" {
			t.Errorf("OldResourceProperties[Foo] = %v, want old-bar", event.OldResourceProperties["Foo"])
		}
		if event.OldResourceProperties["ServiceToken"] != inv.ServiceToken {
			t.Errorf("OldResourceProperties[ServiceToken] = %v, want %q", event.OldResourceProperties["ServiceToken"], inv.ServiceToken)
		}
	})

	t.Run("Update uses OldServiceToken for OldResourceProperties when the service_token changed", func(t *testing.T) {
		t.Parallel()

		engine, invoker, _, _ := newTestEngine(t, func(event crEvent) crResponse {
			return successResponse(event)
		})

		const oldServiceToken = "arn:aws:lambda:us-east-1:123456789012:function:old-handler"

		inv := baseInvocation("Update")
		inv.PhysicalResourceId = "existing-id"
		inv.OldResourceProperties = map[string]any{"Foo": "old-bar"}
		inv.OldServiceToken = oldServiceToken

		_, err := engine.Invoke(context.Background(), inv)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}

		event := invoker.lastCall()
		if event.ResourceProperties["ServiceToken"] != inv.ServiceToken {
			t.Errorf("ResourceProperties[ServiceToken] = %v, want the current %q", event.ResourceProperties["ServiceToken"], inv.ServiceToken)
		}
		if event.OldResourceProperties["ServiceToken"] != oldServiceToken {
			t.Errorf("OldResourceProperties[ServiceToken] = %v, want the OLD token %q, not the current one",
				event.OldResourceProperties["ServiceToken"], oldServiceToken)
		}
	})
}

func TestCustomResourceEngineInvoke_InvokeAPIError(t *testing.T) {
	t.Parallel()

	engine, invoker, _, _ := newTestEngine(t, nil)
	invoker.invokeErr = errors.New("AccessDeniedException: not authorized")

	_, err := engine.Invoke(context.Background(), baseInvocation("Create"))
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "not authorized") {
		t.Errorf("error = %q, want it to contain the underlying invoke error", err)
	}
}

func TestNewCustomResourceRequestID(t *testing.T) {
	t.Parallel()

	id1, err := newCustomResourceRequestID()
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	id2, err := newCustomResourceRequestID()
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if id1 == id2 {
		t.Errorf("expected two distinct RequestIds, got the same value twice: %q", id1)
	}
	if len(id1) != 36 {
		t.Errorf("RequestId %q has length %d, want 36 (UUID-shaped)", id1, len(id1))
	}
}
