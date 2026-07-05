# RFC 005: CloudFormation Custom-Resource Polyfill (`cfncompat_custom_resource`)

|  |  |
|---|---|
| **Status:** | Implemented |
| **Companion:** | `000-PRD.md` (§5 gap G4), `002-architecture.md` (§7.1), `004-intrinsic-function-polyfill.md` |

## 1. Decision

`cfncompat_custom_resource` is a Terraform resource that **emulates the CloudFormation
engine's side of the custom-resource protocol**: it builds the CFN request event, delivers
it to the `ServiceToken` (Lambda async invoke or SNS publish), and waits for the handler's
response PUT to a **pre-signed S3 `ResponseURL`** — so existing CloudFormation custom-resource
handlers (hand-written Lambdas, CDK provider-framework, CDK `AwsCustomResource`) work
**unmodified** when CDK Terrain synthesizes to Terraform/OpenTofu.

Three decisions taken with the project owner:

1. **AWS configuration via `hashicorp/aws-sdk-go-base/v2`** (the awscc pattern): the provider
   schema mirrors terraform-provider-awscc's (static creds, profile, region, shared
   config/credentials files, retries, IMDS opt-out, proxies, `assume_role`,
   `assume_role_with_web_identity`, endpoint overrides for lambda/sns/s3/sts), resolved in one
   `awsbase.GetAwsConfig()` call. **Configure is lenient**: resolution failures are stored in
   `ProviderData.ConfigErr` rather than failing Configure, so the intrinsic-function surface
   (RFC 004) keeps working with a completely unconfigured provider; resources surface the
   stored error when actually used. `SkipCredsValidation` is set — no STS round-trip at
   Configure time.
2. **Protocol-only coverage of `AwsCustomResource`** (arbitrary SDK calls): CDK's
   `AwsCustomResource` synthesizes to a `Custom::AWS` resource whose ServiceToken is a
   CDK-owned singleton Lambda (dynamic JS SDK v3 dispatch, response flattened to dot-path
   `Data` keys). That Lambda deploys through the normal synthesis/asset pipeline like any
   function, so the protocol polyfill covers it with zero extra provider code. A native
   in-provider `cfncompat_aws_sdk_call` (embedded Smithy models + generic wire protocols to
   remove the runtime-Lambda dependency) is **deferred to a future RFC**.
3. **Response transport bucket from provider config**: optional provider attribute
   `custom_resource_bucket` (the analog of CDK's bootstrap bucket), overridable per resource
   via `response_bucket`; a use-time error when neither is set. No provider-managed hidden
   bucket.

Scope note: SNS service tokens are supported alongside Lambda. A `cfn-signal`/WaitCondition
analog (the `tconsaws_signal` SQS blueprint from RFC 002 §7.1) is explicitly **out of scope**
for this iteration.

## 2. Protocol fidelity (what the resource reproduces)

Verified field-for-field against aws-cdk sources (`core/lib/custom-resource.ts`,
`custom-resources/lib/provider-framework/runtime/cfn-response.ts` and
`@aws-cdk/custom-resource-handlers`):

- **Event**: `RequestType` (`Create`/`Update`/`Delete`), `ResponseURL` (pre-signed S3 PUT),
  `StackId`, `RequestId`, `ResourceType`, `LogicalResourceId`, `PhysicalResourceId` (Update/
  Delete only), `ResourceProperties` (with `ServiceToken` merged in, as CFN does),
  `OldResourceProperties` (Update only, carrying the *old* ServiceToken if it changed).
- **Response**: `Status`/`Reason`/`PhysicalResourceId`/`Data`/`NoEcho`; missing
  `PhysicalResourceId` defaults to `RequestId` on Create and the prior id on Update/Delete
  (the CDK framework defaults); responses > 4096 bytes fail (CFN's documented limit); the
  pre-signed URL does not constrain `Content-Type` (handlers send `content-type: ""`).
- **Semantics**: `ServiceTimeout` (1–3600 s, default 3600) bounds response polling; an
  UPDATE returning a **different `PhysicalResourceId` is a replacement** — after recording
  the new state the provider sends a cleanup DELETE with the old id and old properties
  (CFN's `UPDATE_COMPLETE_CLEANUP` phase; failure is a warning, not an error); a FAILED
  CREATE writes no state, so no DELETE is ever sent for a never-created resource
  (equivalent to CFN's `CREATE_FAILED` marker short-circuit); a FAILED DELETE keeps the
  resource in state; `NoEcho` raises a warning that Terraform state cannot be redacted.
- **`Fn::GetAtt` translation**: the response `Data` object is the computed dynamic `data`
  attribute; the synthesis backend renders `Fn::GetAtt` on a custom resource as an index
  into it (AwsCustomResource's flattened dot-path keys, e.g. `Buckets.0.Name`, are literal
  map keys).

Known judgment call (revisit first if Terrain needs different semantics): the replacement
cleanup DELETE dispatches to the **current** service_token/resource definition, carrying only
the old `PhysicalResourceId` and old `ResourceProperties` — mirroring CFN's cleanup phase
being driven by the currently-active template.

## 3. What the research established (evidence)

- **awscc has no deployment-model capabilities**: strictly Cloud-Control-registered types;
  `Custom::*`, WaitCondition and cfn-signal are structurally impossible there. The
  cfncompat niche is unoccupied. Its `internal/generic` engine (ProgressEvent polling,
  taint-on-timeout, name-map translation) and its aws-sdk-go-base Configure block served
  as the design templates.
- **tconsaws** validated the partial-AWS-schema approach but left several declared
  attributes unwired (retry modes, shared files, token) — motivating aws-sdk-go-base,
  where the mapping is a single audited translation function with unit tests.
- **Read is a no-op and import is unsupported** by design: the CFN custom-resource
  protocol has no read/describe operation to rebuild state from.

## 4. Testing

The engine is built on three narrow interfaces (`crLambdaInvoker`, `crSNSPublisher`,
`crResponseStore`) so the full protocol matrix runs as unit tests with fakes (no AWS, no
TF_ACC): create/update/delete success + failure paths, replacement cleanup, timeout,
oversized response, SNS dispatch, ServiceToken merge, OldResourceProperties presence, and
the changed-service-token regression. Acceptance tests are gated on `TF_ACC` +
`CFNCOMPAT_TEST_LAMBDA_ARN` + `CFNCOMPAT_TEST_RESPONSE_BUCKET` against a real handler; the
`endpoints` overrides also allow a LocalStack-backed run.
