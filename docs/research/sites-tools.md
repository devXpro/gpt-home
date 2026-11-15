# ChatGPT Sites tools (codex_apps `sites.*`)

Filled by the orchestrator in T0.5 (2026-11-15). Re-check before later Site tasks: these are Codex-internal tools and may change.

How to produce the data (read-only, touches a real ChatGPT account through the Codex login of this Mac):

```sh
node site/release/release.mjs --list-sites-tools > /tmp/sites-tools.json
```

It prints one JSON object keyed by tool name, each with `description` and `inputSchema`. If no tool name starts with
`sites.` or `sites_`, it logs every `codex_apps` tool name instead and exits 3.

## Run record

| Field | Value |
|---|---|
| Date | 2026-11-15 |
| Codex CLI version | codex-cli 0.163.0 |
| Account / profile | the owner's own Codex login (default profile), personal plan |
| Tool count | 39 `sites.*` tools |

## Tools used by `site/release/release.mjs`

| Tool | Used for | Parameters seen | Notes |
|---|---|---|---|
| `sites.list_sites` | find the Site by slug | `limit`, `role` | |
| `sites.check_slug_availability` | before creating | `slug` | |
| `sites.create_site` | first release | `title`, `slug`, `description`, `creation_intent` | |
| `sites.create_source_repository_write_credential` | push the source | `project_id` | |
| `sites.update_environment_variables` | the settings | `project_id`, `set_values[]` (`key`, `value`, `is_secret`) | |
| `sites.save_version_and_deploy_private` | publish (via one `codex exec` call) | `project_id`, `commit_sha`, `archive` | |
| `sites.get_deployment_status` | wait for the deployment | `project_id`, `deployment_id` | |
| `sites.list_site_versions` | `--settings-only` | `project_id`, `limit` | |
| `sites.deploy_private_site_version` | `--settings-only` | `project_id`, `version_id` | |

## Other `sites.*` tools found

| Tool | Parameters | Might be useful for |
|---|---|---|
| `sites.add_custom_domain` | `project_id`, `hostname` | Add a custom domain to a published site. The response includes a CNAME target for subdomains, A record targets |
| `sites.change_site_slug` | `project_id`, `slug` | Change a site's public URL label. The change runs asynchronously. When the result is pending, use get_site to  |
| `sites.create_schedule` | `project_id`, `request_id`, `task` | Create and link a cloud task for a published Site when the user has requested scheduling. Before creating a ne |
| `sites.delete_site` | `project_id` | Permanently delete a site. Use only when the user explicitly asks to delete the site. This does not unpublish  |
| `sites.deploy_site_version` | `project_id`, `version_id`, `tunnel_bindings`? | Deploy a saved site version to production when the site is shared, public, cannot be verified as owner-only, o |
| `sites.generate_siwc_bypass_token` | `project_id` | Generate a bearer token for identity-less API requests that bypasses a site's Sign in with ChatGPT gate. Call  |
| `sites.get_database_overview` | `project_id`, `binding_name`? | Inspect D1 bindings and tables for the Sites Settings database viewer. |
| `sites.get_database_table_rows` | `project_id`, `table_name`, `binding_name`?, `limit`?, `offset`? | Read one page of database rows for the Sites Settings database viewer. |
| `sites.get_environment` | `project_id` | Deprecated legacy Codex app alias. Use get_environment_variables instead. |
| `sites.get_environment_variables` | `project_id` | Get the production runtime environment variables for a site. These values are separate from local .env files a |
| `sites.get_project` | `project_id` | Deprecated legacy Codex app alias. Use get_site instead. |
| `sites.get_site` | `project_id`, `include_mcp_connection`? | Get a site and its current access configuration, including external visitors. For a Library Site result, copy  |
| `sites.get_site_analytics_overview` | `project_id`, `start_time_ms`, `end_time_ms`, `view`?, `granularity`? | Get overall and hourly or daily page views, unique visitors, and the top five pages for a site analytics dashb |
| `sites.get_site_version` | `project_id`, `version_id` | Get a saved site version and its source provenance. Retain version_id for follow-up calls, but report the user |
| `sites.get_site_worker_logs` | `project_id`, `since_minutes`?, `limit`?, `errors_only`? | Read recent production Cloudflare Worker logs for a Site when diagnosing why a deployed website is crashing, r |
| `sites.list_custom_domains` | `project_id` | List custom domains attached to a site. |
| `sites.list_projects` | `limit`, `cursor`? | Deprecated legacy Codex app alias. Use list_sites instead. |
| `sites.list_site_analytics_events` | `project_id`, `start_time_ms`, `end_time_ms` | List custom event names observed for a site. The requested window must stay within the last 30 days. |
| `sites.list_sites_creator_control_panel_projects` | `cursor`? | List the next page of projects for the Sites control panel. |
| `sites.query_site_analytics_event` | `project_id`, `start_time_ms`, `end_time_ms`, `event_name` | Query daily occurrence counts and unique visitors for one custom site event. The requested window must stay wi |
| `sites.read_database_overview` | `project_id`, `binding_name`? | Inspect the user tables in a deployed site's live Cloudflare D1 database before reading rows. Returns only exa |
| `sites.read_database_table_rows` | `project_id`, `table_name`, `binding_name`?, `limit`?, `offset`? | Read one bounded page of rows from a user table in a deployed site's live Cloudflare D1 database. Call read_da |
| `sites.refresh_custom_domain_status` | `project_id`, `custom_domain_id` | Refresh custom domain validation status for a site. |
| `sites.remove_custom_domain` | `project_id`, `custom_domain_id` | Remove a custom domain from a site. |
| `sites.save_site_version` | `project_id`, `commit_sha`, `archive`? | Save a version of the site's pushed source without deploying it. Full SHA of the pushed source commit. It must |
| `sites.update_access` | `project_id`, `access_mode`, `allowed_user_emails`?, `allowed_workspace_group_ids`?, `allowed_tenant_group_ids`? | Deprecated legacy Codex app alias. Use update_site_access instead. |
| `sites.update_environment` | `project_id`, `set_values`, `remove`? | Deprecated legacy Codex app alias. Use update_environment_variables instead. |
| `sites.update_site_access` | `project_id`, `access_mode`?, `allowed_user_emails`?, `allowed_workspace_group_ids`?, `allowed_tenant_group_ids`?, `editor_changes`?, `viewer_changes`? | Update who can visit a site only when the user asks to change access. Set access_mode only when the user expli |
| `sites.update_site_metadata` | `project_id`, `title` | Update a site's display title. This does not change the site's public URL. |
| `sites.validate_suggested_schedule` | `project_id`, `schedule_suggestion` | Validate a model-proposed cloud schedule for a published Site and return it for the user to set up. Read get_s |

## Answers

- **Rename later:** `sites.update_site_metadata(project_id, title)` changes the display title without touching the URL, so the admin can rename «Мой шалаш» at any time.
- **Visibility:** `sites.update_site_access(project_id, access_mode, allowed_user_emails, …)` with `access_mode` `public`, `workspace_all` or `custom` (email allowlist). Whether sharing a Site this way also lets another personal account connect its *plugin* is untested; OpenAI help says Site access and plugin access are separate. Worth one test with Tanya (it would remove the second Codex login).
- **Public deploy:** `sites.deploy_site_version` deploys to production for shared/public sites and accepts `tunnel_bindings` (aliases exposed to site code as `CUSTOMER_HTTP_<ALIAS>`): OpenAI-side tunnels to private HTTP services. Not needed with our own relay.
- **Delete:** `sites.delete_site` exists, so test Sites can be cleaned up.
- **Quota:** a full publish ends with one `codex exec` model call (`save_version_and_deploy_private`), which spends
  the Codex usage of the account being published to. On 2026-11-15 the owner's account hit "You've hit your usage
  limit … try again at 2:39 PM" exactly there; the Site, the source push and the settings had already succeeded.
  `--settings-only` (save settings + `deploy_private_site_version`) uses no model call, so "Перевыпустить" stays
  cheap. Path B therefore needs a little Codex quota on each family member's account for the first publish and for
  Site-code changes.
- **Plugin name in ChatGPT:** filled after the T0.5 phone test (see `connection-paths.md`).

## Raw dump

Kept locally, not committed: these are Codex-internal schemas (`/tmp/gpthome-spike/sites-tools.json` on the owner's Mac, 2026-11-15). Regenerate with the command above.
