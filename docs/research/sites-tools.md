# ChatGPT Sites tools (codex_apps `sites.*`)

Template only. The orchestrator fills it in T0.5 (and re-checks it before later Site tasks).

How to produce the data (read-only, touches a real ChatGPT account through the Codex login of this Mac):

```sh
node site/release/release.mjs --list-sites-tools > /tmp/sites-tools.json
```

It prints one JSON object keyed by tool name, each with `description` and `inputSchema`. If no tool name starts with
`sites.` or `sites_`, it logs every `codex_apps` tool name instead and exits 3.

## Run record

| Field | Value |
|---|---|
| Date | |
| Codex CLI version | |
| Account / profile | |
| Tool count | |

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
| | | |

## Open questions

- Is there a tool to set the Site's title or visibility after creation?
- Does the listing show the plugin's name as ChatGPT displays it (the prefix `--check` looks for)?

## Raw dump

<!-- Paste the JSON from `--list-sites-tools` here, or link the file. -->
