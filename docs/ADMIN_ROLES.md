# Admin staff roles

Every admin request reads the account's current role and active status from PostgreSQL. UI navigation is not an authorization boundary. Existing Super Admin and broad Marketplace Admin assignments are preserved; new accounts should receive a focused role.

| Role | Access |
| --- | --- |
| Super Admin | All tools; create staff, assign roles, enable/disable access, reset passwords, view access audit, confirm completed bank payouts |
| Marketplace Admin (legacy broad) | Existing marketplace/support tools; no staff management or payouts |
| Support | Support tickets, replies and support notifications |
| Moderation | Provider verification/documents, service/product moderation, reports and complaints |
| Finance | Withdrawal preparation/rejection and transaction records; cannot mark a payout paid |
| Advertising | Existing promotion performance reports; campaign creation/editing is not implemented |

`PATCH /api/v1/admin/admins/:admin_id` changes role and/or `is_active`. Super Admin only. Self-demotion/self-disable is refused, and at least one active Super Admin must remain. Access changes are serialized and the acting Super Admin is revalidated inside the transaction. DELETE disables the account and retains its identity/history. Password resets preserve the existing token lifetime behavior; disabling the account revokes access on subsequent requests.

Migration 106 expands the role constraint and adds an indexed, cursor-paginated access audit. Audit entries contain role/active changes, never passwords or hashes. The admin frontend must follow the backend deployment. No new infrastructure or worker topology is required.

Production release is separate from the promotion-pool release. Before deployment: retain rollback images/settings and a fresh backup, apply/independently verify migration106, then deploy matching worker/API/replicas using the existing attended procedure. Retain the additive audit schema on application rollback; disable newly created focused-role accounts until the compatible backend/UI are restored. Do not restore the production database to roll back UI changes.

Tests: isolated PostgreSQL access creation/login/change/disable/reset/audit and live JWT route authorization across six roles, immediate revocation/unknown-role denial, and finance payout denial with unchanged withdrawal state. CI executes these against PostgreSQL with the race detector. Browser acceptance lives in the admin-panel repository and uses synthetic API responses only.

This provides role separation, not a claim that support staffing, ticket assignment, SLAs, financial reconciliation or million-user capacity are complete.
