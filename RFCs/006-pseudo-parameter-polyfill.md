# RFC 006: Pseudo-Parameter and `Fn::GetAZs` Polyfill (data sources)

|  |  |
|---|---|
| **Status:** | Implemented |
| **Companion:** | `002-architecture.md` (§I2 `IIntrinsicResolver.resolvePseudo`), `004-intrinsic-function-polyfill.md` (§3.6 excluded intrinsics), `005-custom-resource-polyfill.md` (`stack_id` event field) |
| **Origin:** | `s3-notifications-harness/docs/awscc-gaps.md` — backlog items 1–3 |

## 1. Decision

Two **data sources** fill the CloudFormation pseudo-parameter and `Fn::GetAZs` gaps so a CDK
Terrain synthesis targeting `hashicorp/awscc` + `cfncompat` needs **no `hashicorp/aws`** data
sources for `AWS::AccountId` / `AWS::Partition` / `AWS::Region` / `AWS::URLSuffix` /
`AWS::StackName` / `AWS::StackId` / `AWS::NotificationARNs` or for `Fn::GetAZs`:

| Data source | CloudFormation equivalent |
|---|---|
| `data "cfncompat_pseudo_parameters"` | all `AWS::*` pseudo parameters (one call, one STS request) |
| `data "cfncompat_availability_zones"` | `Fn::GetAZs` (incl. the documented EC2-VPC default-subnet behaviour) |

awscc is generated 1:1 from the CloudFormation registry and has no read-only/data-source
concept beyond resource mirrors; hashicorp encourages the dual-provider pattern
([awscc#1974](https://github.com/hashicorp/terraform-provider-awscc/issues/1974#issuecomment-2327416624)).
cfncompat is that second provider for CloudFormation-semantics values.

Explicitly **not** in scope (stay bridge-/CDK-CLI-side): `Ref`/`Fn::GetAtt` (graph wiring),
`Fn::ImportValue`/`Fn::GetStackOutput` (cross-stack model), `AWS::NoValue` (§4), `Fn::Transform`,
`CreationPolicy`/`UpdatePolicy`, assets/bootstrap (`archive_file`, `cdk bootstrap`), and offline
region fact tables (§5).

## 2. `data "cfncompat_pseudo_parameters"` — the `Aws` class as a data source

### 2.1 Why one data source, not one per parameter

aws-cdk-lib mints every pseudo parameter in one accessor (`core/lib/cfn-pseudo.ts:22-29`):

```ts
export class Aws {
  public static readonly ACCOUNT_ID = pseudoString('AWS::AccountId');
  public static readonly URL_SUFFIX = pseudoString('AWS::URLSuffix');
  public static readonly PARTITION  = pseudoString('AWS::Partition');
  public static readonly REGION     = pseudoString('AWS::Region');
  public static readonly STACK_ID   = pseudoString('AWS::StackId');
  public static readonly STACK_NAME = pseudoString('AWS::StackName');
  ...
}
```

and RFC 002 §I2 routes all of them through a single `IIntrinsicResolver.resolvePseudo(name, ctx)`.
One data source gives the bridge one singleton node per stack and one STS call — the same fan-in
as CDK. The alternative (`cfncompat_caller_identity`, `cfncompat_region`, `cfncompat_partition`,
`cfncompat_url_suffix`, `cfncompat_stack`, mirroring `hashicorp/aws` names) was rejected: five
nodes and five generated binding classes for what CDK models as one accessor, `partition`/
`url_suffix` are pure functions of region anyway, and `cfncompat_stack` would be a data source with
no backing API. TerraConstructs (`~/tcons/base/src/aws/aws-stack.ts:246-373`) confirms the
singleton pattern against `hashicorp/aws` today — `DataAwsRegion`, `DataAwsCallerIdentity` and
one `DataAwsPartition` for both `partition` and `urlSuffix`.

### 2.2 Attribute contract

| Attribute | Kind | Semantics |
|---|---|---|
| `stack_name` | optional input, echoed | `AWS::StackName`. The bridge passes `Stack.stackName`. |
| `notification_arns` | optional input (list), echoed; default `[]` | `AWS::NotificationARNs`. The bridge passes `StackProps.notificationArns` (`stack.ts:510-516`); no L2 reads it. |
| `account_id` | computed | `AWS::AccountId` — STS `GetCallerIdentity` (direct `sts` client honouring `endpoints.sts`, behind a fakeable interface) |
| `partition` | computed | `AWS::Partition` — from the region-prefix table; a differing caller-ARN partition only produces a warning |
| `region` | computed | `AWS::Region` — the resolved provider region; error if unresolvable |
| `url_suffix` | computed | `AWS::URLSuffix` — partition → DNS suffix table (see §2.4) |
| `stack_id` | computed | `AWS::StackId` — `arn:<partition>:cloudformation:<region>:<account_id>:stack/<stack_name>/<uuid-v5>`; `null` when `stack_name` is unset |
| `id` | computed | `<partition>:<account_id>:<region>` |

HCL:

```hcl
data "cfncompat_pseudo_parameters" "current" {
  stack_name = "MyApp-Prod"
}

locals {
  fn_arn = "arn:${data.cfncompat_pseudo_parameters.current.partition}:lambda:${data.cfncompat_pseudo_parameters.current.region}:${data.cfncompat_pseudo_parameters.current.account_id}:function:x"
  s3_host = "s3.${data.cfncompat_pseudo_parameters.current.region}.${data.cfncompat_pseudo_parameters.current.url_suffix}"
  export  = "${data.cfncompat_pseudo_parameters.current.stack_name}:ExportsOutputRefBucket"
}

resource "cfncompat_custom_resource" "notifications" {
  stack_id      = data.cfncompat_pseudo_parameters.current.stack_id   # RFC 005 StackId event field
  service_token = ...
}
```

Generated CDK Terrain binding (`cdktn get`; same shape as `DataAwsCallerIdentity` in `@cdktn/provider-aws`):

```ts
export interface DataCfncompatPseudoParametersConfig extends cdktn.TerraformMetaArguments {
  readonly stackName?: string;
  readonly notificationArns?: string[];
}
export declare class DataCfncompatPseudoParameters extends cdktn.TerraformDataSource {
  static readonly tfResourceType = "cfncompat_pseudo_parameters";
  get accountId(): string;          // AWS::AccountId
  get partition(): string;          // AWS::Partition
  get region(): string;             // AWS::Region
  get urlSuffix(): string;          // AWS::URLSuffix
  get stackName(): string;          // AWS::StackName
  get stackId(): string;            // AWS::StackId
  get notificationArns(): string[]; // AWS::NotificationARNs
}
```

Bridge sketch (`TerraformIntrinsicResolver`, RFC 002 I2) — one singleton per stack:

```ts
private pseudo(ctx: IResolveContext): DataCfncompatPseudoParameters {
  const stack = Stack.of(ctx.scope);
  const id = 'CfncompatPseudoParameters';
  return (stack.node.tryFindChild(id) as DataCfncompatPseudoParameters)
    ?? new DataCfncompatPseudoParameters(stack, id, {
         stackName: stack.stackName, notificationArns: stack._notificationArns });
}
resolvePseudo(name: string, ctx: IResolveContext): any {
  const p = this.pseudo(ctx);
  switch (name) {
    case 'AWS::AccountId':        return p.accountId;
    case 'AWS::Region':           return p.region;
    case 'AWS::Partition':        return p.partition;
    case 'AWS::URLSuffix':        return p.urlSuffix;
    case 'AWS::StackName':        return p.stackName;
    case 'AWS::StackId':          return p.stackId;
    case 'AWS::NotificationARNs': return p.notificationArns;
    case 'AWS::NoValue':          return Token.nullValue();   // §4
  }
}
```

### 2.3 `stack_id`: deterministic and stateless — no `cfncompat_stack` resource

There is no CloudFormation stack in an awscc/Terraform deployment, yet `AWS::StackId` must be
**stable across applies**: CDK custom-resource handlers use it as an ownership key. The S3
notifications handler (`@aws-cdk/custom-resource-handlers/lib/aws-s3/notifications-resource-handler/index.py:40-58`)
prefixes every notification `Id` with `f"{stack_id}-"` and, on Delete, classifies existing
notifications as external via `startswith(f"{stack_id}-")` — a changing `stack_id` would orphan
or delete the wrong notifications. The value is therefore a pure function of
`(partition, region, account_id, stack_name)`: the ARN prefix plus a UUID v5 of that prefix
under a fixed cfncompat namespace. It needs no state, no resource, and survives
`terraform state rm`/re-import. ARN shape is kept for protocol fidelity (RFC 005 passes it verbatim
as the event's `StackId`; third-party handlers may parse it). Without `stack_name` there is no
stack identity to derive, so `stack_id` is `null` rather than an invented value.

### 2.3b `notification_arns`: echoed, not delivered

`AWS::NotificationARNs` is the list of SNS topics CloudFormation's *engine* publishes every
stack event to. The Terraform plugin protocol (tfplugin5/6) gives a provider RPCs only for its
own resources (`PlanResourceChange`/`ApplyResourceChange`/…) — there is no "apply started/
finished" RPC and no visibility into other providers' resources — so a cfncompat provider
cannot deliver stack-wide events. The data source therefore only echoes the list (so templates
that read the pseudo parameter still resolve). Delivering events belongs above the provider:

- **CLI wrapper** (recommended, full fidelity today): the cdktn execution backend already owns the
  `terraform`/`tofu` invocation; `apply -json` (OpenTofu: `-json-into=FILE`) streams per-resource
  `apply_start/complete/errored`, `resource_drift`, `diagnostic` events that map 1:1 onto
  CloudFormation stack events and can be published to `notification_arns`.
- **Terraform 1.14 actions**: the synthesis backend generates the whole config and could inject a
  `cfncompat` publish action with `lifecycle { action_trigger }` on every resource — in-band and
  HCL-native, but `create`/`update` only (no destroy events yet).
- Platform notifications (HCP Terraform, Spacelift, env0) are run-level only.

No registry provider offers a `NotificationARNs` analog.

### 2.4 Partition / URL-suffix table

Mirrored from aws-cdk `region-info/lib/aws-entities.ts:90-97` (the table behind
`RegionInfo.get(region).partition/.domainSuffix`):

| Region prefix | Partition | URL suffix |
|---|---|---|
| (default) | `aws` | `amazonaws.com` |
| `cn-` | `aws-cn` | `amazonaws.com.cn` |
| `us-gov-` | `aws-us-gov` | `amazonaws.com` |
| `us-iso-` | `aws-iso` | `c2s.ic.gov` |
| `us-isob-` | `aws-iso-b` | `sc2s.sgov.gov` |
| `us-isof-` | `aws-iso-f` | `csp.hci.ic.gov` |
| `eu-isoe-` | `aws-iso-e` | `cloud.adc-e.uk` |
| `eusc-de-` | `aws-eusc` | `amazonaws.eu` |

The table is authoritative for `partition`, exactly as it is the only source for `url_suffix` (no
AWS API returns that — `hashicorp/aws`'s `aws_partition.dns_suffix` is likewise a static SDK
table). CloudFormation resolves both from the region the stack is deployed to, so deriving them
from the same input keeps `partition`, `url_suffix`, `stack_id` and `id` consistent with
`AWS::Region`. The STS caller ARN carries a partition too, but it never overrides the table: a
disagreement (credentials from one partition, region from another) only raises a warning naming
both.

## 3. `data "cfncompat_availability_zones"` — `Fn::GetAZs`

CDK's environment-agnostic path (`core/lib/stack.ts:919-928`) is exactly the awscc/cdktn case:

```ts
const agnostic = Token.isUnresolved(this.account) || Token.isUnresolved(this.region);
if (agnostic) return [Fn.select(0, Fn.getAzs()), Fn.select(1, Fn.getAzs())];
```

| Attribute | Kind | Semantics |
|---|---|---|
| `region` | optional input | `""`/unset ≡ `AWS::Region` (per the `Fn::GetAZs` reference) |
| `names` | computed list | **the `Fn::GetAZs` value**: `available` zones, restricted to those with a default subnet (`DescribeSubnets` filter `default-for-az=true`), falling back to all available zones when none has a default subnet — CloudFormation's documented EC2-VPC behaviour; alphabetical |
| `all_names` | computed list | every `available` zone of the region, alphabetical; Local/Wavelength Zones excluded. `names` is filtered from it |
| `zone_ids` | computed list | zone ids aligned with `all_names` |
| `id` | computed | the region |

Implementation: EC2 `DescribeAvailabilityZones` + `DescribeAccountAttributes`
(`supported-platforms`, the same platform check CloudFormation makes; EC2-Classic-only accounts
skip the subnet call, and a missing attribute counts as EC2-VPC) + `DescribeSubnets`; every one of
the three is required and a failure of any is a hard error. New `endpoints.ec2` provider override
(LocalStack parity with the existing `lambda`/`sns`/`s3`/`sts`).

```hcl
data "cfncompat_availability_zones" "current" {}                        # Fn::GetAZs ""
data "cfncompat_availability_zones" "euw1"    { region = "eu-west-1" }  # Fn::GetAZs "eu-west-1"

locals {
  az0 = provider::cfncompat::select(0, data.cfncompat_availability_zones.current.names)
  az1 = provider::cfncompat::select(1, data.cfncompat_availability_zones.current.names)
}
```

```ts
// binding
export declare class DataCfncompatAvailabilityZones extends cdktn.TerraformDataSource {
  static readonly tfResourceType = "cfncompat_availability_zones";
  constructor(scope: Construct, id: string, config?: { region?: string } & cdktn.TerraformMetaArguments);
  get names(): string[]; get allNames(): string[]; get zoneIds(): string[];
}
// bridge: Fn::GetAZs → singleton per (stack, region); Fn::Select → provider::cfncompat::select
resolveIntrinsic('Fn::GetAZs', [region], ctx) {
  const stack = Stack.of(ctx.scope);
  const id = `CfncompatAvailabilityZones${(region ?? '').replace(/-/g, '')}`;
  const ds = stack.node.tryFindChild(id) as DataCfncompatAvailabilityZones
    ?? new DataCfncompatAvailabilityZones(stack, id, { region: region || undefined });
  return ds.names;
}
```

TerraConstructs does the same per-region cache against `aws_availability_zones`
(`aws-stack.ts:290-311`, `:548-556` → `Fn.element(azs.names, i)`).

## 4. `AWS::NoValue` — bridge-side, via `Token.nullValue()`

No provider surface. CDK Terrain already has `Token.nullValue()` (`cdktn/lib/tokens/token.ts`),
an `IResolvable` that renders the Terraform `null` keyword and is protected from being dropped in
function-call arguments (`tfExpression.js` `resolveExpressionPart`). It is the right target for
attribute positions — CloudFormation removes the property, Terraform treats `null` as unset.
Caveat the bridge must handle: inside a **list** CloudFormation drops the element
(e.g. `Fn.join('/', [..., prefix ?? Aws.NO_VALUE])`, `aws-codebuild/lib/cache.ts:82`) whereas a
Terraform list keeps a `null` element — the bridge must drop it at synth time or wrap with
`compact()`; `condition_if` returning `NoValue` is the common trigger.

## 5. Why no offline region-fact functions

A `provider::cfncompat::partition(region)` / `url_suffix(region)` pair was considered so the
bridge could emit literals when the region is literal (as `Stack.partition` does under
`ENABLE_PARTITION_LITERALS`, `stack.ts:780-789`). Rejected: consumers (aws-cdk-lib, TerraConstructs)
already carry the maintained `region-info` fact database, and this provider would have to track
it. The bridge keeps CDK's own rule — literal `partition` from `RegionInfo` when `region` is
literal, otherwise the data source — and only `account_id` is ever a deploy-time value.

Related caveat (TerraConstructs): with no literal provider region every stack is effectively
environment-agnostic and several L2 paths throw on a token region (`servicePrincipalName`,
`aws-stack.ts:395-406`; ELBv2 access logs, `base-load-balancer.ts:543-547`). The bridge should
therefore prefer the provider's literal region as `AWS::Region` whenever one is configured and
use `data.cfncompat_pseudo_parameters.*.region` only as the fallback.

### Why `region_facts.go` exists although aws-cdk `region-info` has the same table

The table is consulted at **apply** time inside the Go plugin process, where no TypeScript
package is reachable. Consumers that have `region-info` (aws-cdk-lib, TerraConstructs, the
synthesis backend) use it at synth time to emit literals when the region is literal, and reach
for the data source only when the region is unknown until apply. `aws-sdk-go-v2` exports no
partition metadata, which is why `hashicorp/aws` ships the same static table
(`names/partition.go`). It is the only source for `AWS::URLSuffix`. Eight rows, kept in sync
with `aws-entities.ts`.

### `stack_id` interaction with `cfncompat_custom_resource`

`cfncompat_custom_resource.stack_id` defaults to the shared sentinel `cfncompat/no-stack-id`.
A `null` data-source `stack_id` (no `stack_name`) wired into it is not an error: the Plugin
Framework applies the default to a null config value, so the sentinel is used. That is
predictable but silent, so the resource emits a **warning** whenever `stack_id` is null in
config; the warning is planned to become an error in v1.0 (the provider has no consumers yet).
The resource example shows the intended wiring.

## 6. Testing

- **Unit** (no AWS): STS/EC2 behind narrow interfaces with fakes (the RFC 005 pattern) —
  partition/url-suffix table, region-derived partition (a differing caller ARN only warns),
  `stack_id` determinism and `null` without `stack_name`, `notification_arns` echo/default,
  GetAZs supported-platforms branches, default-subnet filter, fallback and ordering, subnet
  pagination cycles, `region = ""` handling, `ConfigErr` surfacing.
- **Acceptance** (`TF_ACC=1` + credentials): both data sources against real AWS.
- **E2E** (terratest, `integ/`): `fixtures/pseudo_parameters` composing both data sources with
  `select` and `cfncompat_custom_resource.stack_id`; `TestE2EPseudoParameters` gated on
  `CFNCOMPAT_E2E_AWS=1`, new job in `e2e.yml`. Read-only — creates no AWS resources.
