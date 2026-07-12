# RFC-006: Backend-Neutral Synthesis Architecture

**Status:** Proposed (Future Work)

## Summary

This RFC proposes a future evolution of the AWS CDK synthesis
architecture to introduce a backend-neutral Intermediate Representation
(IR).

The current implementation strategy intentionally preserves the existing
AWS CDK synthesis pipeline, minimizing changes to the AWS CDK codebase
while enabling Terraform as an alternative deployment target.

Once Terraform support has proven the viability of the compatibility
approach, the synthesis pipeline can be generalized by introducing a
backend-neutral semantic representation that decouples the CDK
programming model from any specific Infrastructure-as-Code backend.

This RFC is **not a prerequisite** for Terraform support. Instead, it
describes a possible evolution of the AWS CDK architecture after
Terraform compatibility has reached production maturity.

## Motivation

The current AWS CDK architecture assumes CloudFormation as its synthesis
target.

Although this assumption has served the project well, it couples several
synthesis concepts directly to CloudFormation constructs, including
intrinsic functions, references, conditions, outputs, dependencies, and
deployment semantics.

Terraform support demonstrates that many of these concepts are actually
higher-level infrastructure semantics rather than
CloudFormation-specific behavior.

Introducing an Intermediate Representation allows these semantics to be
represented independently from the final deployment backend.

## Goals

-   Preserve the existing AWS CDK programming model.
-   Preserve all existing L1/L2/L3 constructs.
-   Preserve jsii compatibility.
-   Allow multiple synthesis backends.
-   Make CloudFormation one backend rather than the backend.
-   Reduce backend-specific logic inside the synthesis pipeline.
-   Enable future backend experimentation without changing construct
    libraries.

## Non-Goals

This RFC does **not** propose:
- replacing CloudFormation
- deprecating CloudFormation synthesis
- changing construct APIs
- modifying user applications
- introducing breaking changes
- replacing the existing implementation strategy for Terraform support


## Current Architecture

``` text
Construct Tree
        │
        ▼
CloudFormation Synthesis
        │
        ▼
Cloud Assembly
        │
        ▼
CloudFormation Template
```

Terraform support currently integrates into this architecture while
preserving these assumptions.

## Proposed Architecture

``` text
Construct Tree
        │
        ▼
Semantic Intermediate Representation (internal)
        │
        ▼
Cloud Assembly
        │
        ├──────────────┐
        │              │
        ▼              ▼
CloudFormation     Terraform
Serializer         Serializer
        │              │
        ▼              ▼
CloudFormation     Terraform JSON/HCL
```

The Intermediate Representation becomes the canonical internal output of
synthesis.

Cloud Assembly evolves to store and expose the IR, while remaining the
primary artifact consumed by synthesis backends.

Backends are responsible only for serializing the IR into their
respective deployment formats.

## Design Principles

### Backend Neutrality

The IR is an **internal implementation detail** of the AWS CDK synthesis
pipeline. It is not intended to be a public API or extension point.

The IR should not contain CloudFormation-specific or Terraform-specific
concepts. Instead, it models infrastructure semantics such as resources,
dependencies, references, expressions, assets, outputs, parameters, and
lifecycle metadata.

Backend-specific constructs are introduced only during serialization.

### Semantic Preservation

The purpose of the IR is not to translate CloudFormation. Instead, it
preserves the semantics expressed by the construct tree.

### Extensibility

Future synthesis targets should require only:
- a serializer
- runtime-specific expression handling
- runtime-specific deployment semantics

No construct changes should be required.

## Relationship with Terraform Compatibility

The Terraform compatibility project provides an opportunity to validate
which concepts belong in a backend-neutral representation.

Implementations developed for Terraform, such as dependency graphs,
expression trees, resource references, lifecycle metadata, provider
metadata, and asset handling, may inform the design of the internal IR.

## Migration Strategy

### Phase 1

Current approach.

``` text
Construct Tree
↓
Existing CloudFormation synthesis
↓
Terraform compatibility layer
```

No significant AWS CDK architectural changes.

### Phase 2

Introduce the internal IR while preserving existing behavior.

Cloud Assembly evolves to use the IR as its canonical internal
representation.

CloudFormation becomes one serializer over the IR, while Terraform
continues using its serializer.

Applications remain unchanged.

### Phase 3

Additional synthesis backends may be introduced without modifying
construct libraries.

## Benefits

-   Reduced backend coupling.
-   Improved separation of concerns.
-   Better testability through backend-independent semantic validation.
-   Future backend extensibility while preserving the AWS CDK
    programming model.

## Compatibility

This proposal is fully backward compatible.

Existing applications continue using CloudFormation synthesis by
default.

No construct APIs, jsii APIs, or deployment workflows change unless
users explicitly select an alternative backend.

## Prior Art

This proposal is informed by similar architectural approaches adopted by other infrastructure tooling.

### CDK From CloudFormation

The `cdk-from-cfn` project introduces an intermediate representation between CloudFormation templates and generated CDK constructs. While solving a different problem, it demonstrates the value of introducing a canonical semantic model to decouple producers from consumers.

Source: https://github.com/cdklabs/cdk-from-cfn/blob/6d3864bdd07ee7f7ef3ef20e4cb21f2f4d40d070/ARCHITECTURE.MD

### Pulumi CDK Conversion CLI

Pulumi's conversion architecture introduces an intermediate representation (PCL) between source infrastructure definitions and generated programs. This separation enables multiple input formats and multiple output languages while keeping transformation logic centralized.

This RFC applies the same architectural principle to AWS CDK synthesis by introducing a semantic intermediate representation between the construct tree and backend-specific serializers.

Source: https://github.com/pulumi/pulumi-tool-cdk2pulumi/blob/61ca33bd54ef408e5dda6c43d3300cbafd2960fd/specs/conversion.md

## Open Questions

-   How should backend-specific metadata be attached without
    compromising backend neutrality?
-   Which existing synthesis responsibilities should migrate into
    backend serializers?

## Note

This RFC intentionally describes a potential long-term evolution of the
AWS CDK architecture. The Terraform compatibility project does not
depend on this proposal and should continue targeting the current
synthesis pipeline. Experience gained from implementing Terraform
support will inform the evolution of the synthesis architecture and the
internal Intermediate Representation.
