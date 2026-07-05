# Architecture Note: The AWS-CDK ⇄ CDK-Terrain Interface Contract for Dual-Target (CloudFormation + OpenTofu/awscc) Synthesis

**Status:** Lead-architect recommendation. **This note supersedes the architecture in `001-poc.md`.**
**Authors:** Vincent (CDK Terrain) with the AWS CDK team.
**Grounding:** Derived from independent source-code research (`02-independent-research/GROUND-TRUTH.md`, 7 researchers over `aws-cdk-lib` + the generated `awscc` bindings, deliberately run blind to `001-poc.md`) and a 3-proposal → adversarial-critique → synthesis design pass. Empirically cross-checked against the **TerraConstructs** prior art (`~/tcons/base`). Citations are `file:line`; `aws-cdk/` is the symlinked AWS CDK source.

---

## 0. Framing — the agreed ownership split

This is the organizational decision the whole design serves, and it is settled:

- **AWS CDK owns the *seam*.** `aws-cdk-lib` abstracts its direct CloudFormation language bindings behind interface(s); the existing CFN behavior is refactored to become the **default implementation**. Hard requirement: a CloudFormation consumer sees **zero new dependencies and zero behavior change**.
- **CDK Terrain owns the *fill-in*.** A separate package (`@cdktn/aws-cdk-terraform-provider`) implements those interfaces to emit OpenTofu/Terraform — the interpolation resolver, the awscc L1 renderer, the `Fn.*`→TF/polyfill bindings, the `tf.json`+manifest artifact. AWS ships **nothing** TF-specific and will never own a high-level construct library for non-AWS providers.
- **Polyfills** for missing TF capabilities (cidr subnet math with token inputs, the CFN custom-resource protocol) ship as a Terrain-owned custom Terraform/OpenTofu provider with JSII bindings.

The design question this note answers is therefore narrow and concrete: **where is the interface boundary drawn, at what granularity, and how is the CFN coupling abstracted without changing a byte of CloudFormation output?**

---

## 1. Decision

AWS CDK abstracts a **small family of exactly two new interfaces** behind the construct-synthesis seam — **`IReferenceResolver`** (the keystone: the reference-value factory) and **`IIntrinsicResolver`** (the parallel scope-less `Fn.*`/pseudo-parameter factory, with a `requireCapability` hook) — and **reuses three already-public injectable seams unchanged**: `ITokenResolver` (`resolvable.ts:106-123`), `IFragmentConcatenator` (`resolvable.ts:131-136`), and `IStackSynthesizer` (`stack-synthesizers/types.ts:54`, wired via `AppProps.defaultStackSynthesizer`, `app.ts:149`).

The **keystone is solved by DEFERRAL, not replacement**: `CfnReference` keeps storing its construction-time-baked `{Ref}`/`{Fn::GetAtt}` value, and we change **only the same-stack else-branch of `CfnReference.resolve()`** (`private/cfn-reference.ts:145`, `super.resolve(context)`) to consult the resolver — moving *language-specific* value production from construction time to resolve time while leaving cross-stack and identity machinery byte-identical. **Target selection is per-Stack via `Stack.of(context.scope)`** reading a virtual `Stack.referenceResolver` / `Stack.tokenResolver` getter (default = today's CFN constants); there is **no ambient process-singleton** — `CfnReference.resolve` *already* computes `Stack.of(context.scope)` today (`cfn-reference.ts:134`), so the dispatch handle already exists at the exact call site. Everything else the prior PoC wanted as AWS-owned interfaces — L1 snake_case rendering, `awscc_*` type-name mapping, artifact assembly, environment data-sources — is **TF-shaped and lives entirely in the CDK-Terrain synthesizer**, reached through the existing public `IStackSynthesizer`, per the ownership split.

**Granularity verdict:** a small family (2), not one and not six. One interface is too thin — it ignores the *separately eager* `Fn.*`/`Aws.*` baking and mis-locates dispatch on `IResolveContext`, which has no resolver field (§5). Six interfaces is too broad — it forces AWS to own `renderL1`/`assembleArtifact`/`environment`, which are TF-shaped and forbidden by the ownership split. The verified coupling collapses to exactly two AWS-owned factories of language-specific values; the rest is already public or belongs to Terrain.

---

## 2. The AWS-CDK interface surface

AWS adds **two** interfaces in `aws-cdk-lib/core/lib/resolvable.ts` (next to the existing `ITokenResolver`/`IFragmentConcatenator`), plus **two virtual getters on `Stack`**. No other new public surface.

| # | New symbol | Lives in | Signature (sketch) | Refactors (`file:line`) | CFN default = unchanged behavior |
|---|---|---|---|---|---|
| I1 | **`IReferenceResolver`** | `core/lib/resolvable.ts` | `resolveReference(ref: IReferenceTarget, context: IResolveContext): any` where `IReferenceTarget { target: IConstruct; attribute: string; rendering?: ReferenceRendering; typeHint?: ResolutionTypeHint }` | The eager bake in `CfnReference.for` (`cfn-reference.ts:57-71`) + verbatim pass-through in `Intrinsic.resolve` (`intrinsic.ts:59-61`) — keystone seam #4 | `CfnReferenceResolver.resolveReference` returns the **verbatim body lifted from `cfn-reference.ts:59-68`** (`{Ref}`/`{Fn::GetAtt}`/`Fn::Sub`), `logicalId` read from `(target as CfnElement).logicalId` at resolve time. **Byte-identical.** |
| I2 | **`IIntrinsicResolver`** | `core/lib/resolvable.ts` | `resolveIntrinsic(name, args, context): any`; `resolvePseudo(pseudoName, context): any`; `requireCapability(cap, context): void` | `FnBase` (`cfn-fn.ts:487-493,536`); LangExt `addTransform` side-effects (`cfn-fn.ts:538/949/978`); pseudo baking in `Aws.*`/`pseudoString` (`cfn-pseudo.ts:23-29,85`), `ScopedAws` (`cfn-pseudo.ts:53/75`) | `resolveIntrinsic` returns `{[name]:args}` as `FnBase.value` does (`cfn-fn.ts:489`); `resolvePseudo` returns `{Ref:name}` (`cfn-pseudo.ts:85`); `requireCapability('AWS::LanguageExtensions',ctx)` = `Stack.of(ctx.scope).addTransform(...)` (`cfn-fn.ts:538`). **No behavior change.** |
| I3 | **`Stack.referenceResolver`** | `core/lib/stack.ts` (`protected get`) | `protected get referenceResolver(): IReferenceResolver` | The implicit hardwire at `CfnReference.resolve` else-branch (`cfn-reference.ts:145`) | Default returns package-private `CLOUDFORMATION_REFERENCE_RESOLVER`. A pure-CFN `Stack` never overrides it. |
| I4 | **`Stack.tokenResolver`** | `core/lib/stack.ts` (`protected get`) | `protected get tokenResolver(): ITokenResolver` (existing type) | The hardwired constant at `Stack.resolve()` (`stack.ts:680`: `resolver: CLOUDFORMATION_TOKEN_RESOLVER`) | Default returns existing `CLOUDFORMATION_TOKEN_RESOLVER` (`cloudformation-lang.ts:340`); `Stack.resolve()` reads `this.tokenResolver` instead of the literal. Zero CFN change. |

**Why these two and not more.** Seams #1 (`IFragmentConcatenator`), #2 (`ITokenResolver`), #3 (`IStackSynthesizer`) are **already public and injectable**. The research's central truth: those three "easy" seams are **gated** by one hard fact — a reference *is* a CFN object before any resolver runs (`cfn-reference.ts:57-71` + `intrinsic.ts:59-61`). The only genuinely AWS-owned new coupling is the **value factory** for references (I1) and its scope-less twin for `Fn.*`/pseudo (I2). Everything downstream follows mechanically once references and intrinsics produce TF strings instead of CFN objects.

**The "zero CFN-user impact" guarantee.**
1. **No new dependency** — both CFN defaults are package-private inside `aws-cdk-lib` core, importing nothing TF-specific.
2. **No new behavior** — each default is *moved, not rewritten* code from the cited lines, invoked from the same `Stack.resolve()` pass (`stack.ts:676-682`) at the same moment. `logicalId` is a `Lazy.uncachedString` (`cfn-element.ts:69`) already resolved here today — timing and logicalId stability unchanged.
3. **No trippable surface** — defaults are not exported; `CFN_REFERENCE_SYMBOL` and the `referenceTable` singleton (`cfn-reference.ts:86`) are untouched.
4. **Enforcement** — a CI gate runs the full integ/snapshot template corpus and asserts byte-identical `*.template.json` before/after the refactor; merge blocks on any diff.

---

## 3. The CDK-Terrain implementation surface

`@cdktn/aws-cdk-terraform-provider` implements the two AWS interfaces and supplies the TF-shaped renderer via the **existing public `IStackSynthesizer`** — AWS ships nothing TF-specific.

| Terrain symbol | Implements | Behavior (grounded) |
|---|---|---|
| **`TerraformReferenceResolver`** | `IReferenceResolver` (I1) | Looks up the awscc L1 bound to `ref.target` (per-Stack `target→resource` map kept by the L1 emitter), maps `ref.attribute` PascalCase→snake_case, returns the CDKTF interpolation token, e.g. `Arn`→`'${awscc_iam_role.<id>.arn}'` (`iam-role/index.ts:452-453,393`). For `attribute==='Ref'` consults a per-resource Ref→attribute table (no universal awscc `Ref`): Role `Ref`→`role_name` (`iam-role/index.ts:577-578`), ManagedPolicy `Ref`→`policy_arn` (eliminating the `splitArn(this._resource.ref)` hack at `managed-policy.ts:254`). |
| **`TerraformIntrinsicResolver`** | `IIntrinsicResolver` (I2) | Maps the verified table: `Fn::Join`→`join()`/`${a}${b}`, `Split`→`split`, `Select`→`element`, `Base64`→`base64encode`, `ToJsonString`→`jsonencode`, `Length`→`length`, `Sub`→HCL template, `FindInMap`→`m[k1][k2]` when synth-known. `resolvePseudo`: `AWS::Region`→`${data.aws_region.current.name}`, `AWS::Partition`→literal when `ENABLE_PARTITION_LITERALS` (`stack.ts:792-796`) else `${data.aws_partition.current.partition}`. `requireCapability('AWS::LanguageExtensions',…)` is a **no-op** (suppresses `addTransform`, `cfn-fn.ts:538/949/978`); token-valued `Fn::Cidr` and the custom-resource protocol register the **polyfill provider**; `Fn::Transform` throws a clear synth-time error. |
| **`TerraformTokenResolver`** | `ITokenResolver` *(existing seam, reused)* | `new DefaultTokenResolver(TERRAFORM_CONCAT)`, injected via `Stack.tokenResolver` (I4). Reusable almost verbatim because references now yield plain strings — difficulty concentrates in the reference factory, not the resolver plumbing. **Empirically confirmed:** TerraConstructs runs exactly `new DefaultTokenResolver(new StringConcat())` (`tcons base/src/stack-base.ts:21`). |
| **`TerraformConcat`** | `IFragmentConcatenator` *(existing seam, reused)* | `join(l,r) => '${l}${r}'` HCL interpolation instead of `{Fn::Join:['',…]}` (`cloudformation-lang.ts:331`). |
| **`TerraformStackSynthesizer`** + **`AwsccL1Renderer`** | `IStackSynthesizer` *(existing seam, reused)* | `synthesize(session)`: walks `CfnElement`s; the L1 renderer emits `{resource:{awscc_iam_role:{<id>:{…snake_case…}}}}` — deterministic `awscc_<svc>_<res>` type mapping, PascalCase→snake_case keys, `JSON.stringify(doc.toJSON())` for policy `any→string` fields, `DependsOn`→`depends_on`; resolves each block via a `Stack.resolve()` whose getters are the Terrain impls; writes `<id>.tf.json` and registers the artifact (or feeds CDKTF's pipeline — §9). Supports **1:N rendered-element expansion** for `AWS::IAM::Policy` multi-principal decomposition. Skips bootstrap `CfnRule` (flag). |
| **`PolyfillProvider`** (JSII bindings) | *(no AWS interface)* — Terrain-owned custom TF/OpenTofu provider | Covers §7 deployment-model gaps no seam reaches: the CFN custom-resource Lambda protocol, `CreationPolicy`/cfn-signal, and `Fn::Cidr` subnet math with token inputs. Reached **only** through `IIntrinsicResolver.requireCapability` — a *registered* capability, never implicit. |

### 3.1 The `toJsonString` collapse — one primitive, a whole class of L2s

A direct, high-leverage consequence of the keystone, proven by TerraConstructs. Because a resolved reference is a **concatenable interpolation string** (not a structural intrinsic object), serializing *any* definition document with embedded refs collapses to two lines:

```ts
// tcons base/src/stack-base.ts:319-336 — the ENTIRE serializer
toJsonString(obj) = JSON.stringify(Tokenization.resolve(obj, { resolver: DefaultTokenResolver+StringConcat }))
// in-code comment: "unlike cloudformation, Terraform does not need an intrinsic wrapper"
```

In CloudFormation the same document must be **shredded** into `{"Fn::Join":["",[ "...\"Resource\":\"", {"Fn::GetAtt":[...]}, "\"..." ]]}` by `CloudFormationLang`/`minimalCloudFormationJoin`. On the TF side, a Step Functions ASL definition becomes one string literal: `definition = "{...\"Resource\":\"${awscc_sfn_state_machine.x.arn}\"...}"`. TerraConstructs reuses the *same* `toJsonString` across Step Functions ASL (`state-machine.ts:595`), IAM/resource policy docs (`ecr-repository.ts:915`, `table.ts:1322`), EventBridge patterns (`notify/rule.ts:217`), and CloudWatch dashboards. **For our design this means the `aws-iam` policy-document path and the eventual Step Functions / ECS task-def / API-GW-body paths all share one Terrain-side serializer — and it requires no new AWS interface, only the reused `ITokenResolver`+`IFragmentConcatenator`.** This is the sharpest demonstration that the keystone, once solved, unblocks a broad class mechanically.

### 3.2 Contract clauses Terrain must honor (mixed-process safety)

- **Pure resolver / address-only cache.** `IReferenceResolver` impls MUST be pure functions of `(target, attribute, rendering)`. The shared singleton `CfnReference` (cached in `referenceTable`, `cfn-reference.ts:86`) MUST cache only the address it already holds, **never** the rendered value — else a CFN render could leak into a TF render in a mixed app.
- **TokenMap namespace partitioning.** Partition the process-global `TokenMap` string-token namespace by a stable `backendId` (`token-map.ts:31-35`) to avoid marker collisions when CFN and TF stacks share one process. Resolution still dispatches on `Stack.of(context.scope)`, so the map staying global is safe.

---

## 4. The keystone: references

**The problem.** `CfnReference.for` (`cfn-reference.ts:57-71`) eagerly computes the finished `{Ref:logicalId}` / `{'Fn::GetAtt':[logicalId,attr]}` and stores it; `Intrinsic.resolve` (`intrinsic.ts:59-61`) returns it verbatim. So a reference *is* a CFN object before any resolver runs — the #1 break risk.

**The solution: DEFERRAL, not replacement.** `CfnReference.resolve()` (`cfn-reference.ts:131-147`) **already runs at resolve time, already computes `consumingStack = Stack.of(context.scope)` (`:134`), already dispatches the cross-stack `replacementTokens` path first (`:142-146`), and falls through to a single same-stack else-branch: `super.resolve(context)` (`:145`)**. The entire change is that else-branch:

```ts
} else {
  return Stack.of(context.scope).referenceResolver.resolveReference(
    { target: this.target, attribute: this.displayName, rendering: this.refRender, typeHint: this.typeHint },
    context,
  );
}
```

`Reference` already stores the address — `target` (`reference.ts:20`), `displayName` (`reference.ts:27`). The default `CfnReferenceResolver` is the verbatim body from `cfn-reference.ts:59-68`.

**We deliberately KEEP the baked value at construction.** Strictly safer than the prior PoC's "stop storing the baked value": it leaves the constructor, the `singletonReference` identity keyed on `(target, attribute, refRender)` (`cfn-reference.ts:58,92`), and any incidental pre-resolution readers untouched, while still moving language-specific production to resolve time.

**Why the CFN path is byte-identical.** The default reproduces the exact body; it runs in the same `Stack.resolve()` pass at the same moment with the same finalized `logicalId` (`Lazy.uncachedString`, `cfn-element.ts:69`); `singletonReference` caching, `CFN_REFERENCE_SYMBOL`, and `toString()` hints are unchanged.

**Cross-stack refs injected in `prepareApp`.** `prepareApp()` → `resolveReferences()` runs **before** any synthesizer (`synthesis.ts:58`) and assigns per-consumer `replacementTokens` (`cfn-reference.ts:118,142-167`). That machinery is **value-identity-based and untouched** — the `replacementTokens` branch still fires *first* (`:142`); only the same-stack else-branch (`:145`) is rerouted. For the PoC, cross-stack refs are **suppressed/CFN-only** (§6); longer term the `replacementTokens` assignment itself routes a TF replacement (`terraform_remote_state`) through the *same* `IReferenceResolver` — no new surface.

**Why this is the right substrate (and tcons could not be reused).** TerraConstructs makes the keystone look "shallow" by **not having a `CfnReference` at all** — it builds on **CDKTF + bare `constructs`, with zero `aws-cdk-lib` dependency** (`tcons base/package.json:71-80`; 147 `from "cdktf"` imports, 0 `aws-cdk-lib`), so a reference is just a CDKTF attribute token (`role.ts:514: this.roleArn = this.resource.arn`) resolved by CDKTF's own `TokenMap`. **We cannot adopt that** — our hard requirement is a single `aws-cdk-lib` L2 codebase keeping aws-cdk's *own* token system. tcons therefore proves the **end-state** (refs→`${...}` interpolation works, intrinsics map 1:1 to TF functions via `Fn extends cdktf.Fn`) while confirming the keystone is the **real, unavoidable work** for the AWS-owned-seam approach. Deferral is precisely how we reach tcons's end-state without tcons's fork.

**Tradeoffs.** (1) Per-resolve computation replaces a one-time bake; memoize per `(reference, consumingStack)` after first resolve in `referenceTable` (address is stable once `logicalId` is frozen). (2) Internal readers of a baked `.value` are unaffected because we keep it. (3) The genuinely eager site is **not** references but `Aws.*` pseudo-params — §5.

---

## 5. `Fn.*` target selection — rejecting the process singleton

**The scope-less-static problem.** `Fn.join(...)` is a static returning `new FnJoin(...).toString()`, and `Aws.PARTITION = pseudoString('AWS::Partition')` bakes `Token.asString({Ref:name})` **at class-load** (`cfn-pseudo.ts:23-29,85`). Neither has a scope handle at construction. The prior PoC used this to justify an **ambient process-singleton `SynthesisProviderRegistry`**.

**REJECTED — verified.** Construction-time has no scope, but **resolution-time does**, and references/intrinsics only need to choose a target at resolution. Dispatch is **render-time via `Stack.of(context.scope)`**:

- **`Fn.*` (I2).** `FnBase.resolve(context)` already receives an `IResolveContext` with `.scope` and already calls `Stack.of(context.scope).addTransform(...)` (`cfn-fn.ts:536-538`). Change it to return `Stack.of(context.scope).intrinsicResolver.resolveIntrinsic(name, args, context)` and route `addTransform` through `requireCapability`. The static factory only builds a deferred carrier.
- **`Aws.*` pseudo-params.** The **one genuinely eager site**. Convert `pseudoString` to return `Token.asString` of a **lazy `Intrinsic` carrying only the pseudo NAME**, whose `resolve(ctx)` calls `Stack.of(ctx.scope).intrinsicResolver.resolvePseudo(name, ctx)`. By resolve time it is embedded in a resource with a Stack, so `context.scope` exists. The default returns `{Ref:name}` identically; the `displayHint` is preserved. **This is the single most behavior-change-prone conversion** and must be characterization-tested against the pseudo-param snapshot corpus before merge.

**Why `Stack.of(scope)` and NOT a field on `IResolveContext`.** The **public `IResolveContext`** (`resolvable.ts:12-37`) has exactly `{scope, preparing, documentPath, resolve, registerPostProcessor}` — **no resolver field**. The resolver lives on the **internal** `IResolveOptions` (`private/resolve.ts:79`) and `makeContext` (`private/resolve.ts:340-358`) does not copy it into the context. So "carry the renderer on the context" needs a *second* public-surface change; `Stack.of(context.scope)` is the already-used, verified pattern (`cfn-reference.ts:134`) needing zero `IResolveContext` change.

**Why strictly safer than the singleton (JSII).** Mixed CFN+TF apps are correct by construction (each `Stack` resolves via its own getter — no shared mutable state). A process-global set from the JS kernel is invisible/unsafe to Python/Java/.NET kernels and races across `App` instances; per-Stack dispatch is pure construct-tree navigation (`Stack.of` is JSII-safe). Both interfaces use only `any` returns (matching `ITokenResolver.resolveToken`) and already-exported types — no generics/overloads/symbols cross the boundary. The process-global `TokenMap` is not a counterexample: `IResolvable.resolve()` still dispatches on per-call `context.scope`; we partition only its string-token namespace by `backendId`.

---

## 6. PoC slice

**Scope:** `aws-iam`, the four primary L1s — `CfnRole`, `CfnPolicy`, `CfnManagedPolicy`, `CfnInstanceProfile` — plus `OidcProviderNative`, **within a single concrete-env, custom-resource-free stack** (the most defensible first slice).

**Proven (mapped to verified 1:1 mappings):**
1. **Keystone (I1).** `role.roleArn` consumed in-stack resolves to `'${awscc_iam_role.<id>.arn}'`: `role.ts:488` `attrArn`→`.arn` (`iam-role/index.ts:452-453`); `role.ts:508` `getResourceNameAttribute(_resource.ref)`→`role_name` (`:577-578`); `role.ts:609` `attrRoleId`→`role_id` (`:571`); `managed-policy.ts:254` `splitArn(_resource.ref)` hack eliminated by awscc `policy_arn`/`managed_policy_name`.
2. **Intrinsics/pseudo (I2).** `Aws.PARTITION` in ARN building (`principals.ts:486`, `managed-policy.ts:191`) → `data.aws_partition`/literal; `Fn::Join` ARN strings → HCL concat; `addTransform` no-op via `requireCapability`.
3. **Terrain synthesizer.** Emits `<id>.tf.json` with snake_case props and `assume_role_policy_document` as `JSON.stringify(doc.toJSON())` carrying TF-interpolated ARN tokens — the end-to-end proof the keystone unblocks the policy-doc token case (the §3.1 `toJsonString` collapse, IAM instance).
4. **Portability.** `splitLargePolicy` Aspect still fires (`role.ts:613,795`) — construct-tree level, provider-agnostic.
5. **Zero CFN impact.** The *same* stack class with the default synthesizer produces byte-identical `*.template.json` (golden snapshot).

**Deferred (and why):**
- **Cross-stack refs / `Fn::ImportValue`** — injected in `prepareApp` before synthesis; suppress/CFN-only for v1.
- **`OpenIdConnectProvider` legacy custom resource** (`oidc-provider.ts:168`) — use `OidcProviderNative` (`awscc_iam_oidc_provider`). *(tcons confirms OIDC maps to a native resource.)*
- **`AWS::IAM::Policy` multi-principal → N awscc resources** — structural; defer to the 1:N path; use inline policies on Role for the slice.
- **`CfnPolicyConditional.shouldSynthesize()` suppression** (`policy.ts:165-173`) — restructure to defer L1 creation; out of slice.
- **Assets, env-agnostic pseudo-params beyond region/partition, Conditions, `Fn::Cidr`**, and all §7 blockers.

---

## 7. Explicitly out of scope / needs a different runtime model

Deployment-engine assumptions **no renderer seam reaches**; each is a *runtime-model* problem, owned Terrain-side.

| §7 blocker | Evidence | Eventual TF-side strategy |
|---|---|---|
| **CFN custom-resource protocol** (pre-signed `ResponseURL`, CREATE/UPDATE/DELETE, `PhysicalResourceId`) | `custom-resource.ts:197-203`, `cfn-response.ts:24-58` | The *protocol*, not the shape, is CFN. Reimplement as TF-native or ship the polyfill provider (Go) with JSII bindings; reached only via `requireCapability`. **Refinement from tcons (below).** |
| **Cross-stack `Fn::ImportValue` + export-lock** | `refs.ts:364-376`, `cfn-output.ts:168-179` | `terraform_remote_state`/module-output routed through `CfnReference.replacementTokens` → `IReferenceResolver`. **No deploy-time deletion lock** — a known semantic gap. |
| **Nested stacks** | `refs.ts:634-648`, `nested-stack.ts` | TF modules are a rough analog with different plan-time lifecycle; non-trivial. Out of contract. |
| **`Fn::Transform` (macros, SAM)** | `cfn-fn.ts:276-278` | No TF mechanism. `TerraformIntrinsicResolver` throws a clear synth-time error. |
| **Env-agnostic stacks** | `stack.ts:1537-1538` | HCL has no late-bound-at-deploy model. Require concrete env at synth or generate per-pseudo-param TF variables. |
| **Bootstrap version check / assets** | `stack-synthesizer.ts:333-357`, `default-synthesizer.ts:326` | Omit `CfnRule` (flag); assets move into `terraform apply` via `aws_s3_object` + concrete bucket through `IStackSynthesizer.addFileAsset`. *(tcons does exactly this: `TerraformAsset` + on-demand `s3Object`, `aws-asset-manager.ts:118-175`.)* |
| **CFN Conditions** | `cfn-condition.ts:36-53` | Synth-time eval when concrete; else `count`/`for_each`/ternary + locals — not compositionally equivalent. Out of contract. |

### 7.1 Custom resources *evaporate* on awscc — refinement from TerraConstructs

The §7 custom-resource row is **smaller than it looks on awscc**, and tcons shows why:
- **Many "custom resources" become native properties/resources and disappear entirely.** Log retention is a Lambda-backed custom resource in CFN; in tcons it is the native `retention_in_days` property (`tcons cloudwatch/log-group.ts:563-612`) — no custom resource at all. The same holds for OIDC (native) and a range of awscc resources that expose configuration CFN only reached via custom resources.
- **The genuine residual is `CreationPolicy`/cfn-signal and arbitrary-SDK-call (`AwsCustomResource`).** tcons ships a working blueprint: **`terraform-provider-tconsaws`** (Go, `terraform-plugin-framework`), exposing a single `tconsaws_signal` resource that long-polls SQS (`WaitTimeSeconds:20`) for N success signals matching a `signal_id` — replacing `cfn-signal` by having instances send SQS messages (`signal_resource.go:254-372`). This is the direct template for the polyfill provider's signal/SDK-call capability, surfaced through `requireCapability`.
- **Cross-stack-only patterns stay limited** — tcons restricts S3 bucket notifications to the owning stack (`bucket.ts:997-999`), a pragmatic limitation we inherit until the cross-stack story (above) lands.

Net: the custom-resource "blocker" is really *one* polyfill provider plus a list of constructs that no longer need a custom resource once on awscc — a Terrain-side roadmap item, not an interface-contract gap.

### 7.2 `Fn::Cidr` — synth-time math is the primary path, polyfill is the fallback

Refines `001-poc.md`'s "polyfill provider for cidr." tcons computes subnet math in **pure TypeScript at synth time** when the CIDR is concrete (`tcons compute/cidr-splits.ts`, with the comment *"Terraform cidrsubnets() does the same thing, but forces TF Tokens"*), emitting literal CIDR strings; it falls back to `Fn.cidrsubnet()`/`cidrsubnets()` only when the CIDR is a token (IPAM-allocated, `ip-addresses.ts:322-327`). So for the common concrete-VPC case **no polyfill is needed** — `TerraformIntrinsicResolver` resolves `Fn::Cidr` at synth time; the polyfill provider function is reserved for token-valued inputs.

---

## 8. Prior art: TerraConstructs validates the mechanism, motivates awscc

TerraConstructs (`~/tcons/base`) is the **existence proof** that cdk-terrain is a valid AWS-construct synthesis target — and, by its costs, the clearest argument for *this* architecture over a fork.

**What it proves works (reusable):**
- Refs resolve to `${resource.name.attr}` interpolation; the token/intrinsic coupling in CDK L2s is shallower than it looks once references aren't CFN objects.
- The `toJsonString` = resolve+stringify primitive (§3.1) generalizes across Step Functions, IAM policy docs, EventBridge, dashboards.
- aws-cdk's `Token`/`Arn`/`Duration`/`Size` value-objects port cleanly; intrinsics map 1:1 to TF functions.
- Synthesis can be delegated; cross-cutting concerns ride Aspects.

**What makes it unmaintainable — and what we fix:**
- **Aggregate-module mangling.** tcons merges 7+ CDK packages into one `compute` namespace (~150 files spanning ec2+lambda+stepfunctions+elbv2+apigw+appautoscaling). We keep AWS CDK's existing 1:1 module structure untouched.
- **`provider-aws`, not `awscc` — the decisive cost.** tcons targets `@cdktf/provider-aws`, whose hand-authored schema **does not match CloudFormation shapes**, forcing a *fully separate L2 codebase*: IAM trust policy is `assume_role_policy` (string) vs CFN `assumeRolePolicyDocument` (object); inline policies are an *array* vs CFN's *map*; and **one `AWS::S3::Bucket` explodes into nine Terraform resources** (`bucket` + 8 satellites). Because `awscc` is generated from the **same Cloud Control registry as CloudFormation**, its shapes match the CFN L1 — which is exactly what lets us **reuse AWS CDK's single L2 codebase and its L2→L1 property mapping** instead of reimplementing it. *This is the empirical core of the whole proposal: the awscc choice is what makes the AWS-owned-seam (Option 1) viable rather than another fork.*
- **Hand-rolled dependency ordering & cross-env refs.** tcons needs a `TerraformDependencyAspect` (working around CDKTF #2727) and leaves cross-env ARN/name refs as TODOs — mapping onto our §7 cross-stack work. Real engineering, but bounded and Terrain-side.

---

## 9. Open questions for the AWS CDK team

1. **`ArtifactType` extensibility.** Is `@aws-cdk/cloud-assembly-schema`'s `ArtifactType` extensible without forking, or does the CLI error on an unknown `terraform:stack` type? (`_shared.ts:86-87`). Decides whether `IStackSynthesizer` registers a TF artifact vs. bypasses the assembly.
2. **CDKTF integration boundary.** Does CDKTF consume an `ArtifactType` from the Cloud Assembly or bypass it with its own pipeline? Determines whether seam #3 or a parallel pipeline is the right integration.
3. **`cfnPropertyNames` dict status.** Declared at `cfn-resource.ts:93` (per `core/adr/box-api.md:133-143`) but not populated — WIP or deferred? Natural codegen home for a dual snake_case key map.
4. **Pre-resolution `_cfnProperties` walking.** Can `_cfnProperties` be walked pre-resolution for raw PascalCase values? Affects whether any AWS-internal reader depends on baked-value timing (our deferral keeps the baked value precisely to stay safe here).
5. **awscc parity at v1.89.0.** Which resources/properties are missing vs. ahead of CDK's CFN L1? The AWS interface is shape-agnostic (`resolveReference` returns `any`), so drift never touches AWS surface — but parity bounds the Terrain renderer.
6. **`AWS::IAM::Policy` multi-principal decomposition.** Exact 1→N awscc mapping is unverified.
7. **PolicyDocument token carriage.** Does `JSON.stringify(doc.toJSON())` carry token placeholders correctly into awscc string fields? Gates §3.1 end-to-end.
8. **awscc backend identity.** Since awscc calls Cloud Control API (which underpins CFN), is the CFN-vs-TF distinction partly moot at the AWS backend? Affects long-term drift-detection/import semantics.
9. **Surface acceptance.** Will AWS accept two `protected` virtual getters on `Stack` plus two new `resolvable.ts` interfaces as the *entire* TF-enabling surface, with CFN defaults as moved code under a golden-snapshot CI gate?
