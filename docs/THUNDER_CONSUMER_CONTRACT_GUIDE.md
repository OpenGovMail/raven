# Thunder API Consumer Contract Guide for Raven

## Overview

This document describes the Thunder APIs Raven depends on in production.
It is a consumer contract, not a full provider API documentation set.

Use this together with the OpenAPI consumer spec to:

- document exactly what Raven uses,
- detect breaking changes early,
- define a clear change-notification process.

## Scope

In scope:

- APIs called by Raven for authentication, domain validation, user validation, group validation, and group-member resolution.
- Only the request/response fields Raven actually reads.

Out of scope:

- Thunder endpoints Raven does not call.
- Response fields Raven does not rely on.

## Integration Flows

### 1) Socketmap flow

- Authenticates with Thunder via flow API.
- Validates domain, user, and group-address existence.
- Reads the whole OU tree to map each mail domain (an OU handle) to its OU ID, and
  rebuilds that map every `CACHE_TTL_SECONDS`.

### 2) LMTP delivery group resolver flow

- Authenticates with Thunder via flow API using service credentials.
- Resolves groups recursively to members.
- Resolves member users to email addresses using user and OU APIs.

### 3) Thunderbird OAuth client flow

- Thunderbird uses Thunder OAuth/OIDC endpoints for interactive login and token exchange.
- Raven depends on the resulting access token behavior and key-discovery behavior.
- These are client-facing dependencies and must be included in the consumer contract.

## API Summary

| # | Method | Endpoint | Used for |
|---|---|---|---|
| 1 | POST | /flow/execute | Start auth flow |
| 2 | POST | /flow/execute | Complete auth flow |
| 3 | GET | /organization-units, /organization-units/{id}/ous | Map mail domains to OU IDs |
| 4 | GET | /users?filter=... | User existence validation |
| 5 | GET | /groups?filter=... | Group-address validation |
| 6 | GET | /groups | Group name to group ID lookup |
| 7 | GET | /groups/{groupId}/members | Group member resolution |
| 8 | GET | /users/{userId} | Resolve member user profile |
| 9 | GET | /organization-units/{id} | Read an OU's mail domain (its handle) |
| 10 | GET | /oauth2/authorize | Thunderbird authorization redirect/login |
| 11 | POST | /oauth2/token | Thunderbird code-to-token exchange and refresh |
| 12 | GET | /oauth2/jwks | Public key discovery for token verification |

## Endpoint Contracts

### 1) Start Authentication Flow

Method: POST  
Path: /flow/execute  
Used by: socketmap auth, delivery group resolver

Request fields:

| Field | Required | Notes |
|---|---|---|
| applicationId | Yes | Thunder application ID |
| flowType | Yes | Raven sends AUTHENTICATION |

Response fields:

| Field | Required | Notes |
|---|---|---|
| flowId | Yes | Used in step 2 |
| data.actions[0].ref | No | Used as action if present; fallback is action_001 |

Expected status: 200

Example request:

```json
{
	"applicationId": "<app-id>",
	"flowType": "AUTHENTICATION"
}
```

### 2) Complete Authentication Flow

Method: POST  
Path: /flow/execute  
Used by: socketmap auth, delivery group resolver

Request fields:

| Field | Required | Notes |
|---|---|---|
| flowId | Yes | Value from step 1 |
| inputs.username | Yes | Service/system username |
| inputs.password | Yes | Service/system password |
| inputs.requested_permissions | Yes | Raven sends system |
| action | Yes | action ref from step 1 or action_001 fallback |

Response fields:

| Field | Required | Notes |
|---|---|---|
| assertion | Yes | JWT bearer token |

Expected status: 200

Contract rules:

- assertion must be a JWT with 3 segments.
- JWT must include exp claim (Raven uses it for cache expiry).

### 3) List Organization Units

Method: GET  
Path: /organization-units?limit=100 (roots), /organization-units/{id}/ous?limit=100 (children)  
Used by: socketmap domain check and OU ID lookup

Raven walks the whole tree, including OUs whose handle is not a domain, and maps each
handle that is a domain to its OU ID. A domain that is the handle of more than one OU is
left out of the map.

Required headers:

| Header | Required | Notes |
|---|---|---|
| Authorization: Bearer <assertion> | Yes | System token |

Response fields:

| Field | Required | Notes |
|---|---|---|
| organizationUnits[].id | Yes | OU ID used for user/group matching |
| organizationUnits[].handle | Yes | The OU's mail domain, when it is one |
| links[] | Yes | Raven follows `rel: "next"` until it is absent |

Expected statuses:

- 200: page returned. An OU with no children returns 200 with an empty list.

Contract rules:

- `limit` must accept 100 (Thunder rejects anything larger).
- `links[].href` is a path relative to the Thunder base URL.

### 4) Find Users by Username Filter

Method: GET  
Path: /users?filter=...  
Used by: socketmap user validation

Query pattern sent by Raven:

```text
filter=username eq "<local-part>"
```

Required headers:

| Header | Required | Notes |
|---|---|---|
| Authorization: Bearer <assertion> | Yes | Access token from flow |

Response fields:

| Field | Required | Notes |
|---|---|---|
| totalResults | Yes | Zero means user not found |
| users[].id | Yes | Iteration/logging |
| users[].organizationUnit | Yes | Must match domain OU ID |

Expected status: 200

### 5) Find Groups by Name Filter

Method: GET  
Path: /groups?filter=...  
Used by: socketmap group-address validation

Query pattern sent by Raven:

```text
filter=name eq "<group-name>"
```

Required headers:

| Header | Required | Notes |
|---|---|---|
| Authorization: Bearer <assertion> | Yes | Access token from flow |

Response fields:

| Field | Required | Notes |
|---|---|---|
| totalResults | Yes | Zero means group not found |
| groups[].name | Yes | Must match expected group name |
| groups[].organizationUnitId | Yes | Must match domain OU ID |

Expected status: 200

### 6) List Groups

Method: GET  
Path: /groups  
Used by: delivery group resolver (group name to group ID)

Required headers:

| Header | Required | Notes |
|---|---|---|
| Authorization: Bearer <assertion> | Yes | Access token from flow |

Response fields:

| Field | Required | Notes |
|---|---|---|
| groups[].id | Yes | Used to fetch members |
| groups[].name | Yes | Match by group name |

Expected status: 200

### 7) Get Group Members

Method: GET  
Path: /groups/{groupId}/members  
Used by: delivery group resolver

Required headers:

| Header | Required | Notes |
|---|---|---|
| Authorization: Bearer <assertion> | Yes | Access token from flow |

Response fields:

| Field | Required | Notes |
|---|---|---|
| members[].id | Yes | Used for recursive resolution |
| members[].type | Yes | Expected values used by Raven: user, group |

Expected status: 200

Contract rule:

- members[].type semantics for user and group must remain stable.

### 8) Get User by ID

Method: GET  
Path: /users/{userId}  
Used by: delivery group resolver

Required headers:

| Header | Required | Notes |
|---|---|---|
| Authorization: Bearer <assertion> | Yes | Access token from flow |

Raven resolves username/email using first non-empty field in this order:

1. attributes.email
2. email
3. attributes.username
4. attributes.userName
5. username
6. userName
7. attributes.name
8. name

Org-unit fallback fields:

- organizationUnit or organization_unit

Expected status: 200

### 9) Get Organization Unit by ID

Method: GET  
Path: /organization-units/{id}  
Used by: IMAP login domain check and delivery group resolver

Required headers:

| Header | Required | Notes |
|---|---|---|
| Authorization: Bearer <assertion> | Yes | System token |

Response fields:

| Field | Required | Notes |
|---|---|---|
| handle | Yes | The OU's mail domain |

Expected status: 200

Contract rules:

- The handle is the whole mail domain, of any depth. Parents are not fetched.
- A handle that is not a domain (fewer than two labels, or characters outside
  `a-z 0-9 - _`) means the OU has no mail domain.

### 10) OAuth2 Authorize (Thunderbird)

Method: GET  
Path: /oauth2/authorize  
Used by: Thunderbird mail client (interactive authorization)

Required query parameters:

| Parameter | Required | Notes |
|---|---|---|
| response_type | Yes | Expected value: code |
| client_id | Yes | Thunderbird OAuth client ID |
| redirect_uri | Yes | Must match registered callback |
| scope | Yes | Must include scopes required by Thunderbird/Raven login |
| state | Yes | CSRF protection and response correlation |

Optional query parameters:

| Parameter | Required | Notes |
|---|---|---|
| code_challenge | No | Present when PKCE is used |
| code_challenge_method | No | Usually S256 when PKCE is used |

Expected behavior:

- Successful user authentication and consent returns redirect with authorization code.
- Error responses follow OAuth 2.0 conventions with error and error_description.

### 11) OAuth2 Token (Thunderbird)

Method: POST  
Path: /oauth2/token  
Used by: Thunderbird token exchange and refresh

Request expectations:

| Field | Required | Notes |
|---|---|---|
| grant_type | Yes | authorization_code or refresh_token |
| client_id | Yes | Thunderbird OAuth client ID |
| code | Conditionally | Required when grant_type is authorization_code |
| redirect_uri | Conditionally | Required for authorization_code exchange |
| code_verifier | Conditionally | Required when PKCE was used |
| refresh_token | Conditionally | Required when grant_type is refresh_token |

Response fields:

| Field | Required | Notes |
|---|---|---|
| access_token | Yes | Token used by clients and downstream services |
| token_type | Yes | Expected value: Bearer |
| expires_in | Yes | Lifetime in seconds |
| refresh_token | Recommended | Needed for session continuity in client |
| scope | Recommended | Granted scope set |

Contract rules:

- access_token must be a JWT.
- JWT should include exp, iss, aud, and sub claims.
- token_type must remain Bearer.

### 12) OAuth2 JWKS

Method: GET  
Path: /oauth2/jwks  
Used by: token signature verification and key discovery

Response fields:

| Field | Required | Notes |
|---|---|---|
| keys | Yes | Array of public JWKs |
| keys[].kty | Yes | Key type |
| keys[].kid | Yes | Key ID used to select verification key |
| keys[].use | Recommended | Expected signing use (sig) |
| keys[].alg | Recommended | Expected signing algorithm (for example RS256) |
| keys[].n | Conditionally | Required for RSA keys |
| keys[].e | Conditionally | Required for RSA keys |

Expected status: 200

Contract rules:

- key set must include active signing keys for issued access tokens.
- key rotation must preserve overlap long enough for in-flight token validation.

## Common Protocol Expectations

- Authorization scheme: Bearer token on protected endpoints.
- Content-Type for JSON POST requests: application/json.
- Raven calls Thunder over HTTPS.

## Breaking Change Policy

The following are breaking for Raven unless coordinated:

- Remove or rename any endpoint listed in this guide.
- Change HTTP method of any listed endpoint.
- Remove required request fields Raven sends.
- Remove or rename required response fields Raven reads.
- Change semantic meaning of required fields.
- Return non-200 for expected-success calls, except documented 404 on OU tree lookup.
- Return non-JWT assertion, or JWT without exp.
- Change members[].type semantics so user and group can no longer be interpreted.
- Break OAuth authorize request compatibility for Thunderbird required parameters.
- Remove or change required token response fields (access_token, token_type, expires_in).
- Remove required JWKS fields needed for signature validation (keys, kty, kid and RSA material when RSA is used).

Non-breaking examples:

- Add optional response fields.
- Add new endpoints Raven does not use.

## Change Notification Process

Before a breaking change, Thunder should share:

- change summary,
- updated OpenAPI consumer contract,
- sample request/response payloads,
- migration notes,
- rollout timeline.

Recommended lead time: 30 days minimum for breaking changes.

## Verification Strategy

Raven uses this guide and the OpenAPI consumer spec to:

- validate Thunder responses in integration tests,
- detect schema and behavior drift,
- block releases on contract-breaking changes.

## Ownership

- Consumer: Raven team
- Provider: Thunder team
- Source of truth: docs folder and consumer OpenAPI spec in Raven repository

## Versioning

Suggested model:

- Major: breaking changes
- Minor: backward-compatible additions
- Patch: editorial updates

Current version: 1.0.0
