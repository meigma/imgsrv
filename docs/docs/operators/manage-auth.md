---
title: Manage authentication
description: Bootstrap principals, manage API tokens, and configure OIDC publishers.
sidebar_position: 2
---

# Manage authentication

All write operations on `imgsrv` require a bearer principal with the
appropriate role. This page is the operational reference for managing those
principals, their API tokens, and the OIDC provisioning rules that grant
short-lived workload identities the `content-writer` role.

For the conceptual frame, see [Auth model](../concepts/auth-model.md).

## Bootstrap

On startup against a fresh PostgreSQL-backed deployment with no `auth-manager`
principal, `imgsrv` creates a one-time `auth-manager` principal and prints a
single bootstrap API token to standard output. The token is printed exactly
once.

```
imgsrv bootstrap token (one-time): imgsrv_at_…
```

Capture this token immediately. Use it as a bearer credential against
`/v1/auth/*` to perform the initial setup. The default token lifetime is 24
hours; issue a longer-lived auth-manager token before it expires.

If the bootstrap token is lost before being used: with no live
`auth-manager` principal present, restarting `imgsrv` against the same
database mints another bootstrap token. The earlier principal record remains
in place; the new run only adds another token.

## Create a content-writer principal

Using the bootstrap token as `Authorization: Bearer <token>`:

```bash
# 1. Create the service principal.
curl -sf -X POST https://imgsrv.example.com/v1/auth/principals \
  -H "Authorization: Bearer $BOOTSTRAP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"kind": "service", "display_name": "ci-publisher"}'

# Response includes the principal id. Capture it as $PRINCIPAL_ID.

# 2. Look up the content-writer role id.
curl -sf https://imgsrv.example.com/v1/auth/roles \
  -H "Authorization: Bearer $BOOTSTRAP_TOKEN"

# 3. Assign the role to the principal.
curl -sf -X PUT \
  "https://imgsrv.example.com/v1/auth/principals/$PRINCIPAL_ID/roles/$ROLE_ID" \
  -H "Authorization: Bearer $BOOTSTRAP_TOKEN"

# 4. Issue an API token for the principal. expires_at is required.
curl -sf -X POST \
  "https://imgsrv.example.com/v1/auth/principals/$PRINCIPAL_ID/api-tokens" \
  -H "Authorization: Bearer $BOOTSTRAP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name": "ci-publisher-2026q2", "expires_at": "2026-09-01T00:00:00Z"}'
```

The plaintext token is returned exactly once in the response body. Store it
in the publishing system's secret store.

## Rotate an API token

Issue a new token first, deploy it, then revoke the old one:

```bash
# Issue a new token under the same principal.
curl -sf -X POST \
  "https://imgsrv.example.com/v1/auth/principals/$PRINCIPAL_ID/api-tokens" \
  -H "Authorization: Bearer $AUTH_MANAGER_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name": "ci-publisher-2026q3", "expires_at": "2026-12-01T00:00:00Z"}'

# After the new token is in production, revoke the previous one.
curl -sf -X DELETE \
  "https://imgsrv.example.com/v1/auth/api-tokens/$OLD_TOKEN_ID" \
  -H "Authorization: Bearer $AUTH_MANAGER_TOKEN"
```

Token IDs (not the plaintext) are returned by
`GET /v1/auth/principals/{principal_id}/api-tokens`.

## Configure an OIDC publisher

OIDC provisioning rules grant `content-writer` to workload identities that
present a JWT from a trusted issuer. The rule names the issuer, the expected
audience, the JWT claims to forward into the CEL condition, and the
expression that determines whether the identity is accepted.

Every OIDC provisioning rule grants `content-writer` to identities that match
it. There is no per-rule role selection; the role is fixed.

A rule that lets the `meigma/imgsrv` repo publish from its `master` branch
via GitHub Actions:

```bash
curl -sf -X POST https://imgsrv.example.com/v1/auth/oidc-provisioning-rules \
  -H "Authorization: Bearer $AUTH_MANAGER_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "display_name": "GitHub Actions — meigma/imgsrv master",
    "issuer_url": "https://token.actions.githubusercontent.com",
    "audience": "https://imgsrv.example.com",
    "forwarded_claims": ["repository", "ref", "actor"],
    "condition": "claims.repository == \"meigma/imgsrv\" && claims.ref == \"refs/heads/master\"",
    "enabled": true
  }'
```

The audience is enforced before the CEL expression runs; rules cannot bypass
audience verification. Forwarded claims are the only JWT fields exposed to
the CEL expression — claims not listed are not visible to `condition`.

To preview which existing principals a rule edit would affect, call
`POST /v1/auth/oidc-provisioning-rules/{rule_id}/reconciliation` with a
preview body before applying.

## Disable or remove an OIDC rule

Disabling a rule keeps the principal records but prevents the rule from
matching new tokens:

```bash
curl -sf -X PUT \
  "https://imgsrv.example.com/v1/auth/oidc-provisioning-rules/$RULE_ID" \
  -H "Authorization: Bearer $AUTH_MANAGER_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"enabled": false, …}'
```

Deleting a rule with `DELETE` removes the rule and, when followed by a
reconciliation, unassigns the roles from principals provisioned by it. Use
the reconciliation preview to see the affected principals before applying.

## Recover when the auth-manager is locked out

If every API token under every `auth-manager` principal has been revoked or
lost, there is no in-band path to add a new one. Recovery requires direct
PostgreSQL access:

1. Delete the existing `auth-manager` principal rows (or the principal-role
   rows assigning them the role) to drop the database back into the
   "no live auth-manager" state.
2. Restart `imgsrv`. Startup will print a fresh bootstrap token.
3. Re-create operator principals as needed.

This is intentionally not an in-band recovery: the database is the trust
boundary, and a service-path recovery would defeat its purpose. See
[Trust model](../concepts/trust-model.md).
