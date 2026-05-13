---
title: Auth model
description: How identities, roles, and OIDC provisioning rules fit together in imgsrv.
sidebar_position: 2
---

# Auth model

`imgsrv` has a small surface for authentication and authorization, deliberately
designed around two principal sources and two coarse roles. This page covers
the reasoning behind that shape so the operator how-tos can stay prescriptive.

## Two principal sources

A principal is anything that can hold roles and present credentials. `imgsrv`
supports two principal sources:

- **Local API-token principals** are operator-created service identities. They
  receive long-lived bearer tokens issued through `/v1/auth/principals/{id}/api-tokens`
  and stored in the publishing system. The auth-manager bootstrap principal is
  the first such identity in any deployment.
- **OIDC publishers** are identities that arrive at request time bearing a JWT
  from a trusted issuer. They are not created in advance. When a JWT matches a
  provisioning rule, the corresponding principal is materialized and granted
  the rule's roles.

The split exists because the two have different operational profiles. Local
tokens are easy to bootstrap and rotate, suitable for human-managed control
planes and for CI systems where token storage is straightforward. OIDC
publishers fit short-lived workload identities — typically a CI provider like
GitHub Actions — where issuing a JWT per job is preferable to managing a
shared long-lived secret.

## Two roles, two actions

The role model is intentionally minimal:

- `auth-manager` grants `auth.manage`, the action that gates everything under
  `/v1/auth/*`.
- `content-writer` grants `content.write`, the action that gates uploads,
  draft editing, publishing, publish-job retry, and alias mutation.

The matrix is deliberately small. A more granular role system — separate
upload-only, alias-only, or read-only roles — would offer marginal value
because the publishing flow is a single linear sequence and there is no
realistic deployment in which the producer of bytes is not also the producer
of versions.

A principal may hold both roles. Both are typically held by the same identity
only in single-tenant deployments where the same service runs CI and admin.

## Why CEL on JWT claims

OIDC provisioning rules use the [Common Expression Language (CEL)](https://github.com/google/cel-spec)
to decide whether a JWT should provision a principal. A rule supplies:

- An issuer URL that is fetched and trusted by JWKS.
- An audience that the JWT must claim.
- A list of forwarded claims that are exposed to the CEL expression.
- A CEL boolean expression evaluated against those claims.

Every rule grants the same role: `content-writer`. There is no role selector
in the rule; the assumption is that an OIDC publisher is, by definition, a
publisher of content. Granting `auth-manager` over OIDC is intentionally not
supported because the auth-manager surface is the place an administrator
recovers from a misconfigured OIDC rule.

CEL is the right shape for this problem. It is declarative, sandboxed, easy to
audit by reading the rule, and expressive enough to filter on the structured
fields real-world IdPs put in tokens. A rule like
`claims.repository == "meigma/imgsrv" && claims.ref == "refs/heads/master"`
restricts publishing to exactly the workflow runs that should publish.
Encoding that logic in static configuration files instead would be either too
restrictive or too coarse; encoding it in handler code would push policy into
deployments.

The audience claim is handled specially: the audience configured on the rule
is wrapped into the evaluated CEL expression so the operator-supplied
condition is always conjoined with `hasAny(claims.aud, [<audience>])`. There
is no path through the rule that skips audience verification.

## Typical deployment

A common shape:

- One human-managed `auth-manager` principal, with a long-lived API token
  stored in a secret manager. Used for `/v1/auth/*` administration only.
- One or more OIDC provisioning rules that grant `content-writer` to
  short-lived JWTs from a CI provider. CI workflows publish releases without
  ever touching a long-lived imgsrv-specific secret.

When that shape does not fit — for example, a deployment that publishes from a
single long-running daemon rather than from CI — a local `content-writer`
principal with its own API token is the equivalent path.

The operator how-to that walks through configuring both lives at
[Manage authentication](../operators/manage-auth.md).
