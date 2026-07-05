All key cross-report claims verified against source: `tfResourceType = "awscc_iam_role"` (line 393), `assumeRolePolicyDocument: string` (line 15 — required, not `any`), snake_case emission at line 613, and computed attribute getters. One refinement worth noting: in this checkout `assumeRolePolicyDocument` is `required: true` (line 456 comment), which R6 mapped as such. The evidence base is sound. Producing the consolidated map.

---

# Ground-Truth Map: Can AWS CDK L2 Constructs Synthesize to OpenTofu/Terraform (awscc) Instead of CloudFormation?

**Scope.** Evidence-based reconciliation of seven source-code research reports (R1 synth-pipeline, R2 token-system, R3 cfn-resource-codegen, R4 awscc-bindings, R5 fn-intrinsics, R6 aws-iam-l2, R7 platform-gaps) over `aws-cdk-lib` and the CDKTF-generated `.gen/providers/awscc`. Citations are `file:line` / symbol from the reports; spot-checks against source are flagged **[verified-here]**. Paths are relative to `/Users/vincentdesmet/ai/awscdk-cdktn/`; the CDK core lives under `aws-cdk/packages/aws-cdk-lib/core/lib/`.

**Bottom line up front.** The question "could L2 synthesize to awscc by abstracting CFN behind injectable interfaces" splits into three layers of difficulty that the reports agree on:
- **Property/resource shape (L1 data)** — *mechanically* retargetable; PascalCase↔snake_case and `AWS::X::Y`↔`awscc_x_y` are deterministic and verified.
- **References & token resolution** — *hard but bounded*; `CfnReference` bakes CFN intrinsic objects into token values at construction time, which is the single most pervasive coupling.
- **Platform mechanics** (custom resources, cross-stack refs, assets, env-agnostic deploy) — *genuine architectural blockers* with no clean TF equivalent, independent of resource schemas.

---

## 1. End-to-end synthesis trace

The pipeline turns a construct tree into a **Cloud Assembly** (a directory of CFN JSON templates + asset manifest + `manifest.json`). Reconciled from R1 (primary), R2, R3, R7.

```
App.synth()                                   app.ts:299
 └─ Stage.synth()                             stage.ts:273
     └─ synthesize(root, options)             private/synthesis.ts:43-82
         ├─ injectTreeMetadata               (:45)
         ├─ synthNestedAssemblies            (:47)  recurse child.synth()
         ├─ invokeAspects / invokeAspectsV2  (:49-53)  ← splitLargePolicy etc. run here (R6)
         ├─ injectMetadataResources          (:55)  CDKMetadata
         ├─ prepareApp(root)                 (:58)  → resolveReferences()  [CFN cross-stack wiring]
         ├─ validateTree(root)               (:62)
         ├─ new CloudAssemblyBuilder(...)    (:67-69)
         ├─ synthesizeTree(root, builder)    (:73)
         │    └─ for each Stack: construct.synthesizer.synthesize(session)   synthesis.ts:511  ◄── PLUGGABLE
         └─ builder.buildAssembly()          (:77)  writes manifest.json
```

Inside a stack's synthesizer (`DefaultStackSynthesizer.synthesize`, `default-synthesizer.ts:489-527`):
1. `addBootstrapVersionRule()` injects a `CfnRule` + `CfnParameter` (CFN-only, R7).
2. `synthesizeTemplate()` → `Stack._synthesizeTemplate()` (`stack.ts:1168`) → **`Stack._toCloudFormation()`** (`stack.ts:1465`): collects every `CfnElement`, calls each `e._toCloudFormation()` (`cfn-resource.ts:470-544` emits `{Resources:{[logicalId]:{Type, Properties, DependsOn, DeletionPolicy,…}}}`), then `this.resolve(template)` using the hardwired `CLOUDFORMATION_TOKEN_RESOLVER` **[verified-here: `stack.ts:680`]**.
3. `JSON.stringify(...)` written to `<artifactId>.template.json` (`stack.ts:571`, `:1198`); 1 MB CFN limit checked.
4. `emitArtifact()` → `addStackArtifactToAssembly()` (`_shared.ts`) calls `session.assembly.addArtifact(id, { type: ArtifactType.AWS_CLOUDFORMATION_STACK, … })` **[verified-here: `_shared.ts:86-87`]**.

### The single most important fact: where "output = CloudFormation" is decided

There is **no single switch**. The decision is *split across two hard-coupled points*, and both must change for a TF target:

- **(A) The JSON shape** — `Stack._toCloudFormation()` / `CfnResource._toCloudFormation()` emit the CFN template schema (`Resources`, `Type`, PascalCase `Properties`, `DependsOn`, `DeletionPolicy`) with token values already resolved to CFN intrinsics (`{Ref}`, `{Fn::GetAtt}`) by `CLOUDFORMATION_TOKEN_RESOLVER`.
- **(B) The manifest artifact type literal** — `_shared.ts:87` writes `aws:cloudformation:stack` into `manifest.json`. The CLI dispatches on this (`cx-api/lib/cloud-artifact-aug.ts`) to drive CloudFormation deploys.

R1, R3, and R7 independently converge on this: the format is *implicit in the method names and the resolver constant*, not parameterized. The cleanest already-public lever is the **synthesizer object** (`IStackSynthesizer.synthesize(session)`), but it sees a tree whose tokens were *already resolved to CFN intrinsics* during `prepareApp()` — so swapping the synthesizer alone is insufficient (see §2, §3).

---

## 2. Ranked abstraction seams

Every candidate seam across the seven reports, deduplicated and reconciled. **Leverage-to-cost ranking** (top = best ROI). "Difficulty" reflects code blast-radius + whether the interface already exists.

| # | Seam | Location (file/symbol) | Injected interface would look like | Difficulty | What it unblocks |
|---|------|------------------------|-----------------------------------|------------|------------------|
| 1 | **IFragmentConcatenator** *(already exists & injected)* | `resolvable.ts:131-136`; `CLOUDFORMATION_CONCAT` at `cloudformation-lang.ts:331` **[verified-here]** | `join(left,right)` returning `${left}${right}` instead of `{Fn::Join:['',…]}` | **low** | String concatenation in TF interpolation form — *only* if fragments are already plain strings (blocked by CfnReference, seam #4) |
| 2 | **ITokenResolver into Stack.resolve()** | `stack.ts:680` (`resolver: CLOUDFORMATION_TOKEN_RESOLVER`) **[verified-here]**; `ITokenResolver` public at `resolvable.ts:106-123` | make `Stack.resolve()` take/produce a resolver, or `protected getTokenResolver()` | **low (mechanism) / very-high (semantics)** | Controls all 3 resolution paths (resolveToken/String/List). Highest-leverage *single* change, but a TF resolver must know each construct's TF address (depends on #4) |
| 3 | **IStackSynthesizer.synthesize(session)** *(already public & pluggable)* | `types.ts:54` + call site `synthesis.ts:511`; app-level via `AppProps.defaultStackSynthesizer` (`app.ts:149`) | custom synthesizer writes `<id>.tf.json` + `addArtifact(type:'terraform:stack')` | **medium** | End-to-end output replacement *today*, but: (a) `addArtifact` typed to known `ArtifactType` enum; (b) tree already has CFN intrinsics baked in before it runs |
| 4 | **Reference value factory (replace CfnReference)** | `cfn-reference.ts:57-71` (`CfnReference.for`) | `IReferenceFactory.makeRef(target,attr)` → `${awscc_x.name.attr}` string instead of `{Ref}`/`{Fn::GetAtt}` | **very-high** | THE keystone. Hundreds of L1 getter call sites; must map logicalId→TF address + attr PascalCase→snake_case. Unblocks #1, #2, #3 |
| 5 | **renderProperties → target-dispatched / `_toTerraform`** | `cfn-resource.ts:559` (`renderProperties`), `:470` (`_toCloudFormation`); codegen at `iam.generated.ts:2073` (`convertCfnRolePropsToCloudFormation`) | sibling `convertCfn*PropsToTerraform` emitting snake_case; or `_toTerraform()` on CfnElement | **medium (props) / high (full block)** | snake_case property emission. Mechanical because awscc names = snake_case of CFN PascalCase (verified §4) |
| 6 | **ResourceType string registry** | `CfnResource` ctor `type:` field; `CFN_RESOURCE_TYPE_NAME` (`iam.generated.ts:1706`) | `cfnTypeToTf('AWS::IAM::Role') → 'awscc_iam_role'` | **low** | Deterministic type-name mapping. **[verified-here: `tfResourceType="awscc_iam_role"` at `.gen/.../iam-role/index.ts:393`]** |
| 7 | **PolicyDocument serialization** | `policy-document.ts:77/133/286`; consumed at `role.ts:598` | call `JSON.stringify(doc.toJSON())` before awscc string field | **low** | Bridges `any`(CFN) → `string`(awscc) policy fields. `toJSON()` already exists |
| 8 | **L1 attribute extraction (roleName/Arn/Id)** | `role.ts:508/488/609`; `getResourceNameAttribute` `resource.ts:277` | `IL1RoleAttributes{roleName,roleArn,roleId}` from factory | **low** | Per-resource Ref-equivalent mapping. 1:1 verified for IAM (§4) |
| 9 | **PseudoParameterProvider / IEnvironmentTokens** | `cfn-pseudo.ts:21-29`; `stack.ts:1537` (parseEnvironment fallback) | `{accountId,region,partition,urlSuffix,…}` → data-source refs | **medium / high** | env tokens → TF data sources. Aws.* used in *hundreds* of sites; huge blast radius |
| 10 | **L1 Resource Factory (IAM-scoped)** | `role.ts:598`, `policy.ts:180`, `managed-policy.ts:297` | `IL1Factory.createRole(props)` | **medium** | Localizes `new CfnRole(...)` swaps; few call sites per module |
| 11 | **TransformRegistry (addTransform side-effect)** | `cfn-fn.ts:538/949/978` | `context.registerCapability('LanguageExtensions')` no-op for TF | **low** | Suppresses `addTransform('AWS::LanguageExtensions')` for FindInMap/ToJsonString/Length |
| 12 | **BootstrapVersionCheck opt-out** | `stack-synthesizer.ts:333-357`; `generateBootstrapVersionRule` flag | already a boolean flag | **low** | Skip CfnRule/SSM param for TF |
| 13 | **ICustomSynthesis (attachCustomSynthesis)** | `app.ts:360`, `synthesis.ts:244-265` | `onSynthesize(session)` writes supplemental files | **low** | Side-car files (backend config, tfvars) — *cannot* replace a stack's own output |
| 14 | **ILangSerializer (toJsonString/toYamlString)** | `cloudformation-lang.ts:32-77`; `stack.ts` toJsonString | `toJSON/toYAML` TF-aware | **medium** | jsonencode-equivalent serialization |
| 15 | **CrossStackReferenceStrategy** | `refs.ts:118-300` (`resolveValue`); deprecated hook `stack.ts:1511` `prepareCrossReference` | `ICrossStackReferenceStrategy.resolve(producer,consumer,ref)` | **very-high** | Cross-stack wiring (see §7). No clean TF analog |
| 16 | **CustomResourceBackend** | `custom-resource.ts:17-65` (`serviceToken`) | `ICustomResourceBackend.emitResource(...)` | **very-high** | Custom resources (see §7). Protocol, not just shape, is CFN |
| 17 | **ConditionEmitter (Fn::If / CfnCondition)** | `cfn-condition.ts:36-53`, `cfn-fn.ts:327` | `emitConditional(cond,t,f)` → TF ternary/local | **very-high** | CFN Conditions section (see §7) |

**Reconciliation note:** Seams #1, #2, #4 form a dependency chain that all three of R1/R2/R3 independently identified. The "easy" seams (#1 low, #2 low *mechanism*) are gated by the "hard" seam #4. R2 makes the sharpest statement: *"swapping concatenators is one constructor argument… however the concatenator alone cannot handle the case where one side is already a CFN intrinsic object — you must also prevent CfnReference from baking `{Ref:…}` objects in the first place."* This is the central architectural truth.

---

## 3. Token / intrinsic layer

### How pluggable resolution already is

The token system is **genuinely strategy-based** at the interface level (R2 primary, R1/R5 concurring):

- **`IResolveContext`** (`resolvable.ts:12-37`): carries `scope`, `preparing`, `documentPath`, `resolve(x)` for recursion, `registerPostProcessor()`.
- **`ITokenResolver`** (`resolvable.ts:106-123`): three methods `resolveToken / resolveString / resolveList`. **Already a public, injectable interface.**
- **`IFragmentConcatenator`** (`resolvable.ts:131-136`): `join(left,right)`. **Already injected** into `DefaultTokenResolver`.
- The internal `resolve()` (`private/resolve.ts:129-233`) accepts *any* `ITokenResolver`.

So the *machinery* is pluggable. The coupling is in **what is wired**: `CLOUDFORMATION_TOKEN_RESOLVER = new DefaultTokenResolver(CLOUDFORMATION_CONCAT)` **[verified-here: `cloudformation-lang.ts:340`]**, and `Stack.resolve()` hardwires it **[verified-here: `stack.ts:680`]**.

### The precise CFN-only boundary

Two distinct boundaries, reconciled across R2/R3/R5:

1. **Concatenation (soft boundary, seam #1):** `CloudFormationLang.concat()` (`cloudformation-lang.ts:61-77`) emits `{Fn::Join:['',…]}` only when a side is a non-plain value; `minimalCloudFormationJoin` (`:345-373`) optimizes nested joins. Pure CFN, but cleanly swappable.

2. **Reference baking (hard boundary, the blocker):** `CfnReference.for()` (`cfn-reference.ts:57-71`) **stores the finished CFN intrinsic object** (`{Ref:logicalId}` or `{'Fn::GetAtt':[logicalId,attr]}`) as the `Intrinsic` value *at construction time*. `Intrinsic.resolve()` (`intrinsic.ts:59-61`) returns it **verbatim**. Every `Fn.*` in `cfn-fn.ts` does the same (`FnRef`, `FnGetAtt`, `FnJoin`…). **Consequence (R2, severity=blocker): a reference already *is* a CFN object before resolution even runs.** No post-resolution hook can reformat it without a logicalId→TF-address map. This is why seam #4 gates everything.

**Detection logic** `isIntrinsic()` / `isNameOfCloudFormationIntrinsic()` (`cloudformation-lang.ts:382-396`) and `canInspect()` / `isCloudFormationIntrinsic()` (`runtime.ts:229,403-409`) recognize objects whose single key is `Ref` or starts with `Fn::`, and *pass them through mappers unchanged* — so CFN syntax leaks all the way to output. A TF path needs a parallel "is TF expression" concept.

**Contradiction flagged & resolved:** R2's own open-question answers itself correctly — if `CfnReference` were replaced by a `TerraformReference` storing `'${address.attr}'` *strings*, `DefaultTokenResolver.resolveString` would concatenate them fine via `concat.join`; the CFN-specific concatenator is the only other thing needing replacement. So the token layer's difficulty is **entirely concentrated in the reference factory**, not in the resolver plumbing. R1 rates the resolver seam "very-high" and R2 rates it "low (mechanism)"; both are right about different things — the *plumbing* is low-cost, the *address-mapping it depends on* is very-high.

### Consolidated CFN-intrinsic → Terraform mapping table

Categories: **core-fn** (direct TF builtin), **interpolation** (HCL `${}`), **data-source** (needs a `data` block), **synth-time-constant** (resolve during synth), **polyfill-needed** (no clean equivalent). Source: R5 (primary, cross-checked vs `functions-matrix.json`), R1/R4 concurring.

| CFN intrinsic / pseudo-param | Terraform / OpenTofu | Category | Confidence | Notes |
|---|---|---|---|---|
| `Fn::Join(d,list)` | `join(d,list)` or `${a}${b}` | core-fn | verified | functions-matrix:1606 |
| `Fn::Split(d,s)` | `split(d,s)` | core-fn | verified | exact match |
| `Fn::Select(i,list)` | `element(list,i)` | core-fn | verified | **caveat:** `element()` wraps modulo; `Fn::Select` throws OOB |
| `Fn::Base64(d)` | `base64encode(d)` | core-fn | verified | 1:1 |
| `Fn::ToJsonString(o)` [LangExt] | `jsonencode(o)` | core-fn | verified | suppress `addTransform` (`cfn-fn.ts:949`) |
| `Fn::Length(a)` [LangExt] | `length(a)` | core-fn | verified | suppress `addTransform` (`cfn-fn.ts:978`) |
| `Fn::Sub(body,vars)` | HCL template / `format()` | interpolation | verified | `${LogicalId.Attr}` needs logicalId→TF-address remap |
| `Fn::Ref(id)` | `resource_type.name.id` / `var.name` | interpolation | verified | **core translation problem** (logicalId→address) |
| `Fn::GetAtt(r,a)` | `resource_type.name.attr` | interpolation | verified | attr PascalCase→snake_case |
| `AWS::AccountId` | `data.aws_caller_identity.current.account_id` | data-source | verified | breaks env-agnostic model |
| `AWS::Region` | `data.aws_region.current.name` | data-source | verified | TF provider always concrete |
| `AWS::Partition` | `data.aws_partition.current.partition` | data-source / synth-time-constant | verified | static when region known + `ENABLE_PARTITION_LITERALS` (`stack.ts:792-796`) |
| `AWS::URLSuffix` | `data.aws_partition.current.dns_suffix` | data-source | verified | `stack.ts:803` always emits token |
| `AWS::NoValue` | `null` / omit | synth-time-constant | verified | dynamic-via-Fn::If needs TF conditional |
| `AWS::StackName` | `var.stack_name` | synth-time-constant | likely | no TF builtin; needs injected var |
| `AWS::StackId` | **none** | polyfill-needed | verified | circular via `aws_cloudformation_stack` |
| `AWS::NotificationARNs` | **none** | polyfill-needed | verified | CFN-only |
| **`Fn::FindInMap(m,k1,k2[,def])`** | `m[k1][k2]` / `lookup(lookup(...))` | core-fn | likely | **only when keys synth-time-known**; token keys = no equivalent; `def` variant needs LangExt |
| **`Fn::Cidr(ip,count,mask)`** | `cidrsubnets(prefix,newbits…)` | polyfill-needed | likely | **concrete ip/count → synth-time loop OK; token-valued ip → custom provider function**. `newbits = 32 - mask - prefixLen` (IPv4). VPC construct uses this — feasibility hinges on whether VPC CIDR is concrete at synth (open Q) |
| **`Fn::GetAZs(region)`** | `data.aws_availability_zones.available.names` | data-source | verified | **structural mismatch: data block not inline fn.** Rare in practice — `stack.availabilityZones` (`stack.ts:914-931`) short-circuits to context lookup |
| **`Fn::ImportValue(name)`** | `data.terraform_remote_state…` / module outputs | polyfill-needed | likely/uncertain | **no deploy-time export-lock equivalent** (see §7) |
| `Fn::GetStackOutput(...)` [LangExt] | **none** (or `data.aws_cloudformation_stack`) | polyfill-needed | verified | CDK-specific; weak/cross-acct refs |
| `Fn::Transform(macro)` | **none** | polyfill-needed | verified | **hard blocker** (CFN macros / SAM) |
| `Fn::If/And/Or/Not/Equals` + `CfnCondition` | HCL ternary / `count` / local bools | polyfill-needed | likely | **only if inputs concrete at synth**; named-Conditions-section model has no TF analog |
| `Fn::RefAll/ValueOf/ValueOfAll` | **none** | polyfill-needed | low-severity | CFN Rules-section SSM helpers; not used in L2 resource props |

**Highlighted hard cases:** `Fn::Cidr` (subnet math semantics differ + token-valued input), `Fn::GetAZs` (data-block vs inline), `Fn::FindInMap` (synth-time-only), `Fn::ImportValue` (cross-stack protocol). The arithmetic/string intrinsics are the *easy* majority; the structural ones cluster in networking, mappings, and cross-stack.

---

## 4. awscc as an L1 target — verified verdict

**Verdict: awscc is a viable dual-target L1 for the resource-data layer, with three named caveats.** This is the most strongly-evidenced section (R4 primary, R3/R6 concurring; multiple **[verified-here]** spot-checks).

### Property-shape & casing determinism — VERIFIED

- **Resource type:** `AWS::<Svc>::<Res>` → `awscc_<svc>_<res>` (lowercase, `::`→`_`, drop `AWS::`). **[verified-here: `tfResourceType = "awscc_iam_role"` at `.gen/.../iam-role/index.ts:393`]**. Confirmed across 5 resources (IAM Role, ManagedPolicy, InstanceProfile, S3 Bucket, Lambda) in R4.
- **Property casing:** CFN PascalCase = awscc snake_case, both derived from the **same Cloud Control API schema**. **[verified-here: `assume_role_policy_document` emitted at `iam-role/index.ts:613`]**. R4 verified every `CfnRole` property: `managedPolicyArns`→`managed_policy_arns`, `maxSessionDuration`→`max_session_duration`, `permissionsBoundary`→`permissions_boundary`, etc. Conversion is mechanical (insert `_` before each uppercase, lowercase).
- **Config interface:** awscc `*Config` uses camelCase TS keys mirroring `CfnXxxProps` — so the *TypeScript* surface aligns; divergence is only in emitted JSON key casing and a few types.

### Reference / attribute semantics differences — the real divergences

1. **No universal `Ref`.** CFN `Ref` returns a *resource-type-specific* value (IAM Role→name; ManagedPolicy→ARN; S3→bucket name). awscc has **no `Ref`**; each value is a named computed attribute. **[verified-here: `getStringAttribute('role_name')` at `iam-role/index.ts:578`, `arn` at `:453`, `role_id` at `:572`]**. R4 verdict: *this mapping is not inferrable mechanically — it requires per-resource annotation.* GetAtt-style attributes *do* map 1:1 (`attrArn`→`.arn`, `attrRoleId`→`.roleId`).

2. **Policy documents: `any` (CFN) vs `string` (awscc).** **[verified-here: `assumeRolePolicyDocument: string` (required) at `iam-role/index.ts:15`]** vs `any | IResolvable` in `CfnRoleProps`. awscc requires pre-serialized JSON. Bridgeable via `JSON.stringify(doc.toJSON())` (seam #7) — but **tokens embedded in the document must resolve to TF interpolation, not CFN intrinsics** (depends on seam #4).

3. **Tags.** CFN uses `TagManager` + `CfnTag{Key,Value}` (required, PascalCase). awscc uses `IamRoleTags[]{key?,value?}` (optional, snake_case). Minor shape difference, but **CDK's Aspects-based tag *propagation* fires on `CfnResource` instances, not `TerraformResource`** — so stack-level `Tags.of(scope).add()` would not propagate to awscc resources without a parallel mechanism.

4. **Schema version skew.** awscc (v1.89.0) exposes Cloud Control properties absent from CDK's CFN L1 (e.g. Lambda `capacity_provider_config`, `publish_to_latest_published`). awscc tracks CC schemas on its own cadence. Generally additive (awscc ahead), not a blocker, but means parity is version-dependent (open Q).

### The per-property IAM Role evidence (verified table)

| CFN (`CfnRole`) | awscc (`IamRole`) | Status |
|---|---|---|
| `CFN_RESOURCE_TYPE_NAME='AWS::IAM::Role'` | `tfResourceType='awscc_iam_role'` | **verified-here** |
| `assumeRolePolicyDocument: any\|IResolvable` | `: string` (JSON) | verified — type bridge needed |
| `managedPolicyArns?: string[]` | `managedPolicyArns?: string[]` | verified 1:1 |
| `maxSessionDuration?: number` | same | verified 1:1 |
| `permissionsBoundary?: string` | same | verified 1:1 |
| `roleName?: string` | same | verified 1:1 |
| `policies[].policyDocument: any` (required) | `policies[].policyDocument?: string` (optional) | verified — type + optionality diverge |
| `attrArn` (`Fn::GetAtt Arn`) | `.arn` (`getStringAttribute('arn')`) | **verified-here** 1:1 |
| `attrRoleId` (`Fn::GetAtt RoleId`) | `.roleId` (`role_id`) | **verified-here** 1:1 |
| `.ref` → role name | `.roleName` (`role_name`) | **verified-here** — semantic remap, not Ref |

**Caveated verdict:** awscc is a clean dual-target L1 **for the resource data plane**. The caveats — (a) per-resource Ref→attribute mapping table needed, (b) policy-doc string serialization + token resolution, (c) no tag-propagation — are bounded engineering, not architecture. ManagedPolicy is the *easiest* case (CFN `Ref`=ARN, awscc has explicit `policyArn`, eliminating the `splitArn(.ref)` hack at `managed-policy.ts:254`).

---

## 5. L1 codegen — emitting a dual renderer

**Source generator:** `@aws-cdk/spec2cdk` (in the separate `cdklabs/awscdk-service-spec` repo, a devDependency; `gen` npm script). It reads `@aws-cdk/aws-service-spec`, which is derived from the **CloudFormation Resource Provider Schemas / Cloud Control API** — *the same source awscc is generated from.* This shared origin is why the property sets align (R3, R4).

**The right delegation point.** R3 identifies it precisely: each generated L1 already routes through one virtual method, `renderProperties()` (`cfn-resource.ts:559`), called from exactly one place (`_toCloudFormation` `:493`). The generator already emits a per-resource `convertCfnXxxPropsToCloudFormation()` with hardcoded PascalCase keys (`iam.generated.ts:2073`).

**What it would take:**
1. **Property mapper (low-effort, mechanical):** emit a sibling `convertCfnXxxPropsToTerraform()` with snake_case keys per resource (and per nested struct — this multiplies surface area: e.g. `iamRolePoliciesToTerraform`). Because awscc names = snake_case of CFN PascalCase (verified §4), no new value-mappers are needed for scalars; the runtime mappers (`runtime.ts:20-23`) are identity functions today.
2. **Dispatch (medium):** generalize `renderProperties` to target-aware, or add `_toTerraform()` parallel to `_toCloudFormation()`. The `PostResolveToken` callback that merges `rawOverrides` needs a TF equivalent.
3. **Type-name constant (low):** emit `TF_RESOURCE_TYPE_NAME='awscc_iam_role'` alongside `CFN_RESOURCE_TYPE_NAME`, or a lookup function.
4. **Attribute getters (low):** emit TF-form attribute accessors (the awscc bindings already show the target shape).
5. **`cfnPropertyNames` dict** (`cfn-resource.ts:93`, described in `core/adr/box-api.md:133-143`) is declared but **not populated** in current generated files — it would be the natural home to co-locate the TF snake_case key per property. *Open whether this is WIP or deferred.*

**Note on overrides:** `addOverride`/`addPropertyOverride` (`cfn-resource.ts:313`) hardcode `Properties.`-prefixed CFN paths with PascalCase. A TF retarget either drops user overrides (silently loses customization) or remaps PascalCase→snake_case for nested paths (fragile). No clean answer.

---

## 6. aws-iam as first module

aws-iam synthesizes to four L1s: `CfnRole`, `CfnPolicy`, `CfnManagedPolicy`, `CfnInstanceProfile`, plus two OIDC variants. R6 primary, R3/R4 concurring.

### Verified CFN-machinery dependencies

| Dependency | Location | Retarget verdict |
|---|---|---|
| `CfnRole` with `assumeRolePolicyDocument: any` | `role.ts:598` | needs `JSON.stringify` bridge (seam #7) |
| `roleName` via `getResourceNameAttribute(_resource.ref)` | `role.ts:508`, `resource.ts:277` | use `awscc.roleName`; **cross-env Lazy wrapping** is CFN export/import |
| `roleArn` via `attrArn` (`Fn::GetAtt`) | `role.ts:488` | 1:1 → `.arn` |
| `roleId` via `attrRoleId` | `role.ts:609` | 1:1 → `.roleId` |
| `managedPolicyArn` via `.ref` (=ARN) | `managed-policy.ts:228` | use `.policyArn`; eliminates Ref-as-ARN assumption |
| `managedPolicyName` via `splitArn(.ref)` | `managed-policy.ts:254` | use `.managedPolicyName`; eliminates the hack |
| Policy name from **CFN logicalId** Lazy | `policy.ts:158` | logicalId is CFN-specific → use `Names.uniqueResourceName()` (already non-CFN) |
| `CfnPolicyConditional.shouldSynthesize()` override | `policy.ts:165-173` | **no TF primitive**; restructure to not create L1 when empty/unattached |
| `splitLargePolicy` **Aspect** creating ManagedPolicy | `role.ts:610-619/821` | **portable** — Aspects work at construct-tree level; thresholds are AWS-IAM limits, provider-agnostic |
| `Aws.PARTITION` in ARN building | `principals.ts:486`, `managed-policy.ts:191` | → `data.aws_partition` or synth-time constant |
| `Role.fromLookup` via `CC_API_PROVIDER` | `role.ts:303` | context provider (synth-time); TF would use `data.aws_iam_role` |

### The 'no custom resources' claim — VERDICT: **mostly true, with one exception**

- **`OidcProviderNative`** (`oidc-provider-native.ts:247`) uses native `CfnOIDCProvider` → maps cleanly to `awscc_iam_oidc_provider`. **No custom resource.**
- **`OpenIdConnectProvider`** (legacy, `oidc-provider.ts:168`) **IS a Lambda-backed `CustomResource`** (`Custom::AWSCDKOpenIdConnectProvider`). This is the *one* custom-resource path in aws-iam. Its own source says "DO NOT ADD NEW FEATURES" and points to the native variant (`oidc-provider.ts:108`).

So the claim holds **if** the retarget targets `OidcProviderNative` (the intended migration path). The four primary IAM resources have no custom resources.

### What won't cleanly retarget

1. **`CfnPolicyConditional.shouldSynthesize()`** — post-synthesis suppression has no TF analog; requires constructor restructuring (defer L1 creation). Severity: medium.
2. **Cross-env attribute Lazies** (`getResourceNameAttribute`/`getResourceArnAttribute`) — bake in CFN export/import on cross-environment detection. Severity: medium (see §7).
3. **`AWS::IAM::Policy` → multiple awscc resources.** CFN `Policy` attaches to many principals at once; awscc has *per-principal* `awscc_iam_role_policy` / `_user_policy` / `_group_policy`. One CfnPolicy = N awscc resources (confidence: uncertain). This is a **structural** divergence, not just naming.
4. **`Aws.PARTITION`** tokens (low severity; concrete for known-env stacks).

---

## 7. Platform gaps (no clean TF equivalent)

These are *not* schema/shape issues — they are deployment-model architecture. R7 primary, R1/R5/R6 concurring.

| Gap | Evidence | Severity | Nature of a TF-side solution |
|---|---|---|---|
| **CFN Custom Resource protocol** (pre-signed `ResponseURL` callback, CREATE/UPDATE/DELETE lifecycle, `PhysicalResourceId`) | `custom-resource.ts:197-203`; `cfn-response.ts:24-58` (HTTP-PUT to ResponseURL); `framework.ts:28`; provider-base Lambda+IAM as raw CfnResources | **blocker** | No equivalent. The *protocol* (not just the resource) is CFN. ~100 built-in constructs use custom resources internally (log retention, cert validation, S3 notifications, cross-region refs). Options: rewrite each as TF-native resource, write a real TF provider plugin (Go), or `null_resource`+`local-exec`+CLI shim. All are reimplementation, not retarget |
| **Cross-stack refs: `Fn::ImportValue` + export-lock** | `refs.ts:364-376`; `cfn-output.ts:168-179` | **high** | `terraform_remote_state` data source or module-output wiring. **No deploy-time lock** — TF won't stop deletion of a referenced producer. Requires shared backend config known at synth, conflicting with CDK's late-bound stack names |
| **`Fn::GetStackOutput`** (weak/cross-acct/cross-region) | `refs.ts:541-610` | **high** | Calls `cloudformation:DescribeStacks`. TF analog (`data.aws_cloudformation_stack`) only works if producer *stays* CFN. Cross-region uses `ExportWriter`/`ExportReader` *custom resources* (Lambda+Step Functions) — compounds the custom-resource blocker. **Possible TF reframe: provider aliases** (multi-region providers, no runtime Lambda) |
| **Nested stacks** (`AWS::CloudFormation::Stack` + `CfnParameter` passthrough + `Fn::GetAtt Outputs.*`) | `refs.ts:634-648`; `nested-stack.ts` | **high** | TF modules are a rough analog but resolved at plan-time with different lifecycle and output-reference mechanics. Non-trivial automated translation |
| **Assets / bootstrap-bucket / asset manifest** | `default-synthesizer.ts:326` (`cdk-${Q}-assets-${AWS::AccountId}-${AWS::Region}`); `stack-synthesizer.ts:173-213` (S3 coords in `Fn::Sub`) | **medium** | `IStackSynthesizer.addFileAsset()` seam exists. Emit `aws_s3_object` + concrete bucket; upload moves *into* `terraform apply` instead of pre-deploy `cdk-assets`. Bucket name must be concrete (needs known env) or use data sources |
| **Bootstrap version check** (`AWS::SSM::Parameter::Value<String>` param + `CfnRule`) | `stack-synthesizer.ts:333-357` | **medium** | `CfnRule` (Rules section) is a CFN-only pre-deploy assertion; no TF config-level equivalent. Simply omit (flag already exists, seam #12) |
| **Env-agnostic stacks** (pseudo-params left as tokens when no account/region) | `stack.ts:1537-1538` | **medium** | TF HCL has no late-bound-at-deploy model; all values concrete/variable/data-source at plan. Either require concrete env at synth (breaks the pattern) or generate TF variables per pseudo-param |
| **CFN Conditions** (`Fn::If/And/Or/Not/Equals` + `CfnCondition`) | `cfn-condition.ts:36-53`; `cfn-fn.ts:288-398` | **high** | Synth-time evaluation when inputs concrete; otherwise `count`/`for_each`/ternary + local bools — not compositionally equivalent to the named-Conditions-section + `{Condition:name}` model |
| **`Fn::Transform`** (CFN macros, SAM) | `cfn-fn.ts:276-278` | **blocker** | No TF mechanism. Rare in aws-cdk-lib L2s (mainly SAM); treat as unsupported escape hatch |

---

## 8. Top risks & open technical questions

### What a PoC would most likely break on (ranked)

1. **`CfnReference` baking CFN objects (seam #4).** Every `bucket.bucketArn`-style reference is *already* a `{Ref}`/`{Fn::GetAtt}` object before any resolver runs (`cfn-reference.ts:57-71`, `intrinsic.ts:59-61`). Without replacing the reference factory + a logicalId→TF-address map + attr PascalCase→snake_case, a TF synthesizer receives un-reinterpretable CFN intrinsics. **This is the highest-probability break.** *(R1, R2, R3 all flag as blocker.)*

2. **Cross-stack references injected during `prepareApp()`** — *before* any synthesizer runs (`synthesis.ts:58` → `refs.ts`). A custom synthesizer always inherits a tree with `CfnOutput`/`Fn::ImportValue`/`CfnParameter` constructs already created. A TF path must suppress or reinterpret them.

3. **Custom resources in "innocent" L2s.** Many constructs pull in Lambda-backed custom resources transitively (cert validation, log retention). A PoC scoped to "simple" resources may still hit one.

4. **Policy-document tokens.** `JSON.stringify(doc.toJSON())` works *only* if embedded ARN/ref tokens resolve to TF interpolation, not CFN intrinsics — i.e. it depends on #1.

5. **`addTransform` side-effects** firing silently on a CFN `Stack` scope even in a TF resolve pass (`cfn-fn.ts:538/949/978`) — needs the no-op capability seam (#11).

6. **`Fn::Cidr` in VPC** with token-valued CIDR — no inline TF function returns a variable-length subnet list.

### Genuinely unknown from code alone

- **Is `@aws-cdk/cloud-assembly-schema` `ArtifactType` extensible** without forking? It's a compiled npm dependency, separately versioned; the CLI silently skips/errors on unknown types. (R1)
- **What does CDKTF expect from the cloud assembly** — does it consume an `ArtifactType` or bypass the assembly entirely with its own pipeline? (R1) *This determines whether seam #3 or a parallel pipeline is the right integration.*
- **Can `_cfnProperties` (`protected readonly`) be walked pre-resolution** to get raw PascalCase values before intrinsics substitute? (R1)
- **awscc parity vs CFN at v1.89.0** — which resources/properties are missing or ahead? (R3, R4)
- **Does `JSON.stringify` of a `toJSON()`'d PolicyDocument carry CDKTF token placeholders correctly** through to awscc string fields? (R4, R6)
- **`AWS::IAM::Policy` multi-principal → multiple awscc resources** — exact decomposition unverified (R6, confidence: uncertain).
- **Is the awscc provider itself CFN-backed** (it calls Cloud Control API, which underpins CFN) — making the CFN-vs-TF distinction partly moot at the AWS backend? (R4)
- **`cfnPropertyNames` dict** — WIP or intentionally deferred? Determines the natural codegen home for dual keys. (R3)
- **`TokenMap` is a process-singleton** (`token-map.ts:31-35`) — would a mixed CFN+TF process need per-backend token-map isolation? (R2)

### Synthesis judgment (objective)

The code makes **easy**: resource shape, casing, type-name mapping, scalar property serialization, the synthesizer plug point, the fragment concatenator, attribute getters, and the codegen delegation point — all already-injectable or mechanically deterministic.

The code makes **hard**: reference resolution (one keystone seam, `CfnReference`, gating three "easy" seams), and the env/pseudo-parameter token model (huge blast radius but conceptually clean).

The code makes **architecturally blocked** (independent of any seam): custom resources (protocol, not shape), cross-stack export/import semantics, nested stacks, and `Fn::Transform`. These are deployment-engine assumptions, and no amount of L1/L2 abstraction reaches them — they would need a *different runtime model*, not a different renderer.

A PoC retargeting **within-stack, custom-resource-free, concrete-env aws-iam** (the four primary resources via `OidcProviderNative`) is the most defensible first slice: it exercises seams #4–#8 with verified 1:1 mappings and sidesteps every §7 blocker. The reports do not establish that the *general* L2 surface retargets cleanly — only that this constrained slice has no verified blocker.
