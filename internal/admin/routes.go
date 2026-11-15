package admin

// table lists every endpoint of ADMIN-API.md that T2.3 serves, relative to /api/admin. The passkey ceremonies
// (/auth/*, POST /me/passkeys/begin|finish) and the socket's /local/* are T2.2's (Options.AuthRoutes, LocalRoutes).
// [P3] endpoints are not served yet and answer 404.
func (s *Server) table() []route {
	return []route{
		// §3.1 bootstrap and session
		{"GET /bootstrap", levelPublic, webOnly, s.getBootstrap},
		{"GET /session", levelSession, webOnly, s.getSession},

		// §3.3 me
		{"PATCH /me", levelSession, webOnly, s.patchMe},
		{"GET /me/passkeys", levelSession, webOnly, s.listMyPasskeys},
		{"PATCH /me/passkeys/{id}", levelSession, webOnly, s.renameMyPasskey},
		{"DELETE /me/passkeys/{id}", levelSelfStepUp, webOnly, s.deleteMyPasskey},
		{"GET /me/sessions", levelSession, webOnly, s.listMySessions},
		{"DELETE /me/sessions/{id}", levelSession, webOnly, s.deleteMySession},
		{"POST /me/sessions/revoke-others", levelSession, webOnly, s.revokeOtherSessions},

		// §3.4 overview
		{"GET /overview", levelOwner, both, s.getOverview},

		// §3.5 plugins and tools
		{"GET /plugins", levelOwner, both, s.listPlugins},
		{"GET /plugins/preview", levelOwner, both, s.previewPlugin},
		{"POST /plugins/check-updates", levelOwner, both, s.checkUpdates},
		{"POST /plugins", levelOwnerStepUp, both, s.installPlugin},
		{"GET /plugins/{id}", levelOwner, both, s.getPlugin},
		{"PATCH /plugins/{id}", levelOwner, both, s.patchPlugin},
		{"DELETE /plugins/{id}", levelOwnerStepUp, both, s.removePlugin},
		{"POST /plugins/{id}/restart", levelOwner, both, s.restartPlugin},
		{"POST /plugins/{id}/update", levelOwnerStepUp, both, s.updatePlugin},
		{"GET /plugins/{id}/inventory", levelOwner, both, s.getInventory},
		{"GET /plugins/{id}/logs", levelOwner, both, s.pluginLogs},
		{"GET /tools", levelOwner, both, s.listTools},
		{"POST /tools/{name}/calls", levelOwner, both, s.startCall},
		{"GET /calls", levelOwner, both, s.listCalls},
		{"GET /calls/{id}", levelOwner, both, s.getCall},
		{"DELETE /calls/{id}", levelOwner, both, s.cancelCall},
		{"GET /jobs", levelOwner, both, s.listJobs},
		{"GET /jobs/{id}", levelOwner, both, s.getJob},

		// §3.6 audit
		{"GET /audit", levelOwner, both, s.listAudit},
		{"GET /audit/{id}", levelOwner, both, s.getAudit},

		// §3.7 people and keys (phase 2 part)
		{"GET /people", levelOwner, both, s.listPeople},
		{"GET /people/{id}", levelOwner, both, s.getPerson},
		{"GET /keys", levelOwner, both, s.listKeys},
		{"POST /people/{id}/keys", levelOwnerStepUp, both, s.createKey},
		{"DELETE /keys/{id}", levelOwnerStepUp, both, s.deleteKey},

		// §3.9 settings, relay, system
		{"GET /settings", levelOwner, both, s.getSettings},
		{"PATCH /settings", levelOwner, both, s.patchSettings},
		{"GET /relay", levelOwner, both, s.getRelay},
		{"POST /relay/check", levelOwner, both, s.checkRelay},
		{"PUT /relay", levelOwnerStepUp, both, s.putRelay},
		{"GET /system", levelOwner, both, s.getSystem},
		{"POST /system/check-update", levelOwner, both, s.checkHubUpdate},
		{"POST /system/update", levelOwnerStepUp, both, s.updateHub},

		// §4 events
		{"GET /events", levelSession, both, s.streamEvents},
	}
}
