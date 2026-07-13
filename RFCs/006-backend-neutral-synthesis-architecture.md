# RFC-006: Backend-Neutral Synthesis Architecture (Revised)

**Status:** Proposed (Future Work)

> **Note:** This RFC describes a possible long-term evolution of the AWS
> CDK synthesis architecture. It is **not** a prerequisite for Terraform
> compatibility. The current compatibility work continues to target the
> existing synthesis pipeline with minimal changes to the AWS CDK
> codebase.

------------------------------------------------------------------------

# Summary

This RFC proposes evolving the AWS CDK synthesis pipeline toward a
backend-neutral architecture by introducing an **internal Intermediate
Representation (IR)** between the construct tree and backend
serializers.

Unlike the shared Infrastructure Intermediate Representation (IIR)
proposed in **Planning RFC-06**, the AWS CDK IR remains an **internal
implementation detail**. It is not exposed as a public API and is free
to evolve alongside the AWS CDK implementation.

To maximize interoperability across construct ecosystems, the internal
AWS CDK IR **MUST extend and remain compatible with the public
Infrastructure Intermediate Representation (IIR)** defined in Planning
RFC-06. Backend-neutral concepts are inherited from the shared IIR,
while AWS CDK may introduce additional internal semantic information
required by its synthesis pipeline.

This approach preserves the existing AWS CDK programming model while
allowing CloudFormation and Terraform to become peer synthesis backends.

------------------------------------------------------------------------

# Motivation

Today, the AWS CDK assumes CloudFormation as its synthesis target. This
assumption is reflected throughout the synthesis pipeline, where
semantic concepts such as references, intrinsic functions, conditions,
outputs and deployment semantics are represented directly using
CloudFormation constructs.

While this architecture has served AWS CDK extremely well, it makes
introducing alternative deployment backends more difficult because
backend-specific concepts become intertwined with semantic
infrastructure modeling.

The Terraform compatibility effort demonstrates that most construct
semantics are independent of CloudFormation. Resources, references,
dependencies, expressions and assets all exist before a CloudFormation
template is produced. Only the final serialization step requires
backend-specific knowledge.

Separating semantic synthesis from backend serialization improves
modularity, testability and extensibility without changing the construct
programming model.

------------------------------------------------------------------------

# Background

Planning RFC-06 introduces a **Shared Infrastructure Intermediate
Representation (IIR)** that models cloud-neutral infrastructure
semantics.

That RFC is intentionally platform-oriented and is owned by CDKTN.

This RFC describes how AWS CDK could consume those concepts while
preserving its existing implementation philosophy.

The AWS CDK does **not** expose the shared IIR directly.

Instead:

-   AWS CDK defines an **internal IR**.
-   The internal IR extends the shared IIR.
-   AWS-specific semantic information is layered on top of the shared
    model.
-   Cloud Assembly evolves to store the internal IR.
-   Backend serializers consume the internal IR.

This preserves AWS CDK implementation flexibility while ensuring
interoperability with the broader construct ecosystem.

------------------------------------------------------------------------

# Goals

The goals of this proposal are:

-   Preserve the existing AWS CDK programming model.
-   Preserve all existing L1, L2 and L3 construct APIs.
-   Preserve jsii compatibility.
-   Decouple semantic synthesis from backend serialization.
-   Support multiple synthesis backends.
-   Keep backend-specific logic outside semantic synthesis.
-   Reuse the public IIR defined by Planning RFC-06.

------------------------------------------------------------------------

# Non-Goals

This proposal does **not**:

-   Replace CloudFormation.
-   Deprecate CloudFormation synthesis.
-   Introduce breaking changes.
-   Change construct APIs.
-   Require existing applications to migrate.
-   Expose the internal IR as a supported public API.

------------------------------------------------------------------------

# Current Architecture

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

CloudFormation concepts appear throughout synthesis, making it difficult
to introduce alternative deployment targets without backend-specific
adaptations.

------------------------------------------------------------------------

# Proposed Architecture

``` text
Construct Tree
        │
        ▼
AWS Internal IR
 (extends Shared IIR)
        │
        ▼
Cloud Assembly
        │
 ┌──────┴───────────────┐
 ▼                      ▼
CloudFormation      Terraform
 Serializer         Serializer
        │              │
        ▼              ▼
CloudFormation     Terraform JSON/HCL
```

The internal IR becomes the canonical representation stored inside Cloud
Assembly. Serializers become responsible only for translating semantic
concepts into backend-specific deployment artifacts.

------------------------------------------------------------------------

# Relationship to Planning RFC-06

Planning RFC-06 defines a **Shared Infrastructure Intermediate
Representation (IIR)** intended to be cloud-neutral and reusable across
construct ecosystems.

This RFC intentionally builds upon that work rather than introducing an
incompatible representation.

The AWS internal IR **MUST** satisfy the following requirements:

1.  It **extends** the shared IIR rather than replacing it.
2.  Every semantic concept defined by the shared IIR must be
    representable without loss of meaning.
3.  AWS-specific synthesis information may be added where required.
4.  Backend serializers should primarily consume shared IIR concepts,
    using AWS-specific extensions only when necessary.

Conceptually:

``` text
Shared Infrastructure IIR (Planning RFC-06)
                ▲
                │
      extends / specializes
                │
         AWS Internal IR
                │
         Cloud Assembly
                │
      CloudFormation / Terraform
```

This relationship ensures that AWS CDK remains compatible with the
broader CDKTN ecosystem while retaining freedom to evolve its own
implementation details.

------------------------------------------------------------------------

# Design Principles

## Internal Implementation

The AWS IR is intentionally **not** a public API. Keeping it internal
avoids long-term compatibility constraints and allows the AWS CDK team
to refine the implementation based on practical experience.

## Shared Semantic Foundation

The shared IIR defined by Planning RFC-06 provides the common semantic
vocabulary. The AWS IR builds upon that vocabulary rather than
redefining it.

## Backend Neutrality

Semantic synthesis should avoid assumptions about CloudFormation or
Terraform. Backend-specific constructs are introduced exclusively during
serialization.

## Evolution of Cloud Assembly

Cloud Assembly should evolve from being primarily a container for
CloudFormation artifacts into the canonical container for the AWS
Internal IR.

Rather than storing backend-specific deployment artifacts as the primary
synthesis output, Cloud Assembly becomes the semantic hand-off point
between construct synthesis and backend serialization.

This evolution preserves the role of Cloud Assembly while making it
independent of any single deployment technology.

``` text
Construct Tree
      │
      ▼
AWS Internal IR
      │
      ▼
Cloud Assembly
      │
 ┌────┴─────────────┐
 ▼                  ▼
CloudFormation   Terraform
Serializer       Serializer
```

## Backend Serializers

Backend serializers are responsible for translating semantic
infrastructure concepts into deployment-specific representations.

CloudFormation serialization maps the AWS Internal IR into
CloudFormation templates while preserving existing synthesis behavior.

Terraform serialization maps the same semantic model into Terraform
JSON/HCL without requiring construct libraries to understand
Terraform-specific concepts.

Serializers should remain stateless wherever practical and should avoid
introducing additional semantic transformations beyond those required by
the target backend.

## Benefits

Separating semantic synthesis from backend serialization produces
several architectural benefits.

It isolates backend-specific complexity, improves unit testing by
allowing semantic validation independently of serialization, enables
multiple deployment engines without changing construct libraries, and
reduces long-term coupling between AWS CDK and CloudFormation.

Because the AWS Internal IR extends the shared IIR, improvements made to
common infrastructure semantics can benefit multiple construct
ecosystems.

## Migration Strategy

### Phase 1

Continue the current Terraform compatibility implementation with minimal
changes to the existing AWS CDK synthesis pipeline.

This validates backend compatibility while minimizing implementation
risk.

### Phase 2

Introduce the AWS Internal IR as an implementation detail.

Cloud Assembly evolves to persist the IR while CloudFormation
serialization continues to produce identical templates.

Terraform serialization begins consuming the same semantic
representation.

### Phase 3

Backend-neutral synthesis becomes the standard internal architecture.

Additional backends can be introduced by implementing serializers rather
than modifying construct libraries.

## Relationship to Other RFCs

### Planning RFC-06 -- Shared Infrastructure Intermediate Representation

Planning RFC-06 defines the public semantic foundation shared across
construct ecosystems.

This RFC defines how AWS CDK specializes that foundation through an
internal IR while remaining semantically compatible.

### Planning RFC-07 -- Azure Construct Library Integration

Azure validates the shared IIR through an independently developed
construct ecosystem.

Together, the Azure and AWS implementations demonstrate that
backend-neutral infrastructure semantics are broadly reusable.

### Compatibility RFCs 002--005

RFCs 002--005 remain the implementation strategy for Phase 1.

This RFC intentionally does not replace them. Instead, it describes a
future architectural evolution informed by the experience gained while
implementing those RFCs.

## Alternatives Considered

### Continue Using CloudFormation as the Internal Model

Maintaining CloudFormation as the canonical synthesis representation
minimizes implementation effort but perpetuates tight coupling between
semantic synthesis and deployment technology.

### Introduce Separate Backend Pipelines

Independent synthesis pipelines for CloudFormation and Terraform would
duplicate significant logic and increase maintenance costs.

### Public AWS IR

Exposing the AWS Internal IR as a public API was considered but
rejected. Doing so would introduce long-term compatibility obligations
and reduce implementation flexibility.

Using an internal IR that extends the shared public IIR provides a
cleaner separation of concerns.

## Risks and Mitigations

The primary risk is divergence between the shared IIR and the AWS
Internal IR.

This is mitigated by requiring that the AWS Internal IR extends, rather
than replaces, the shared IIR. Backend-neutral concepts should always
originate in the shared model, while AWS-specific extensions remain
implementation details.

A second risk is increasing synthesis complexity. This is mitigated by
introducing the architecture incrementally while preserving existing
synthesis behavior.

## Open Questions

-   Which portions of Cloud Assembly should become direct
    representations of the AWS Internal IR?
-   How should backend-specific metadata be attached without polluting
    shared semantic concepts?
-   Which existing synthesis responsibilities should migrate into
    serializers over time?
-   What validation tooling should exist to ensure semantic
    compatibility between the shared IIR and the AWS Internal IR?

## Success Criteria

This proposal will be considered successful when:

-   CloudFormation remains the default synthesis backend with no
    observable behavior changes.
-   Terraform consumes the same semantic representation as
    CloudFormation.
-   The AWS Internal IR remains compatible with the shared IIR.
-   Backend implementations require minimal changes to construct
    libraries.
-   Future backend experimentation occurs through serializers rather
    than synthesis redesign.

## Prior Art

The architectural direction proposed here aligns with proven approaches
adopted by other infrastructure tooling.

Projects such as [cdk-from-cfn](https://github.com/cdklabs/cdk-from-cfn/blob/6d3864bdd07ee7f7ef3ef20e4cb21f2f4d40d070/ARCHITECTURE.MD) demonstrate the value of introducing
intermediate semantic models between producers and consumers, while
[Pulumi CDK Conversion CLI](https://github.com/pulumi/pulumi-tool-cdk2pulumi/blob/61ca33bd54ef408e5dda6c43d3300cbafd2960fd/specs/conversion.md) uses an intermediate representation to
separate semantic analysis from target generation.

This RFC applies the same architectural principle to AWS CDK synthesis
while preserving backward compatibility and leveraging the shared
Infrastructure Intermediate Representation defined by Planning RFC-06.

## Conclusion

This RFC presents a long-term evolution of the AWS CDK synthesis
architecture.

By introducing an internal AWS IR that extends the shared Infrastructure
Intermediate Representation, the AWS CDK can preserve its existing
programming model while enabling backend-neutral synthesis. Cloud
Assembly evolves into the canonical semantic artifact, backend
serializers become responsible for deployment-specific translation, and
construct libraries remain focused on expressing infrastructure intent.

Most importantly, this evolution is incremental. Terraform compatibility
does not depend on it, but the lessons learned from compatibility work
provide the evidence needed to evolve the synthesis architecture in a
measured, backward-compatible manner.
