package server

import (
	"net/http"
)

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth", s.authInfo)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.auth(s.me))
	mux.HandleFunc("GET /api/audit", s.admin(s.auditList))

	// agent endpoints
	mux.HandleFunc("GET /api/agent/binary", s.serveAgentBinary)
	mux.HandleFunc("GET /api/agent/install.sh", s.serveAgentInstallScript)
	mux.HandleFunc("GET /api/agent/tunnel", s.handleAgentTunnel)

	// global settings
	mux.HandleFunc("GET /api/endpoints", s.auth(s.listEndpoints))
	mux.HandleFunc("POST /api/endpoints", s.admin(s.saveEndpoint))
	mux.HandleFunc("DELETE /api/endpoints/{name}", s.admin(s.deleteEndpoint))
	mux.HandleFunc("GET /api/endpoints/{name}/agent-install", s.admin(s.agentInstall))
	mux.HandleFunc("GET /api/registries", s.admin(s.listRegistries))
	mux.HandleFunc("POST /api/registries", s.admin(s.saveRegistry))
	mux.HandleFunc("DELETE /api/registries/{name}", s.admin(s.deleteRegistry))
	mux.HandleFunc("GET /api/settings", s.admin(s.getSettings))
	mux.HandleFunc("GET /api/settings/access", s.admin(s.accessInfo))
	mux.HandleFunc("POST /api/settings/access/test", s.admin(s.accessTest))
	mux.HandleFunc("PUT /api/settings", s.admin(s.putSettings))
	mux.HandleFunc("POST /api/settings/rotate-token", s.admin(s.rotateToken))
	mux.HandleFunc("GET /api/templates", s.auth(s.listTemplates))
	mux.HandleFunc("GET /api/templates/compose", s.auth(s.templateCompose))
	mux.HandleFunc("GET /api/tunnels", s.auth(s.listTunnels))
	mux.HandleFunc("PUT /api/tunnels/{id}", s.admin(s.updateTunnel))
	mux.HandleFunc("DELETE /api/tunnels/{id}", s.admin(s.closeTunnel))
	mux.HandleFunc("GET /api/exposures", s.auth(s.listExposures))
	mux.HandleFunc("PUT /api/exposures/{id}/access", s.admin(s.updateExposureAccess))
	mux.HandleFunc("DELETE /api/exposures/{id}", s.admin(s.deleteExposure))
	mux.HandleFunc("GET /api/shortcuts", s.admin(s.shortcutsInfo))
	mux.HandleFunc("PUT /api/shortcuts", s.admin(s.putShortcuts))
	mux.HandleFunc("GET /api/cloudflare", s.admin(s.cloudflareInfo))
	mux.HandleFunc("PUT /api/cloudflare", s.admin(s.connectCloudflare))
	mux.HandleFunc("DELETE /api/cloudflare", s.admin(s.disconnectCloudflare))
	mux.HandleFunc("GET /api/alerts", s.auth(s.alertsStatus))
	mux.HandleFunc("GET /api/alerts/config", s.admin(s.alertsConfig))
	mux.HandleFunc("POST /api/alerts/rules", s.admin(s.saveAlertRule))
	mux.HandleFunc("DELETE /api/alerts/rules/{id}", s.admin(s.deleteAlertRule))
	mux.HandleFunc("POST /api/alerts/channels", s.admin(s.saveAlertChannel))
	mux.HandleFunc("DELETE /api/alerts/channels/{id}", s.admin(s.deleteAlertChannel))
	mux.HandleFunc("POST /api/alerts/test", s.admin(s.testAlertChannel))

	e := func(pattern string, h http.HandlerFunc) {
		method, path, _ := cut(pattern)
		mux.HandleFunc(method+" /api/e/{ep}"+path, s.auth(h))
	}
	e("GET /info", s.info)
	e("GET /df", s.diskUsage)
	e("GET /events", s.events)
	e("POST /prune", s.systemPrune)

	e("GET /containers", s.listContainers)
	e("POST /containers", s.createContainer)
	e("GET /containers/{id}", s.inspectContainer)
	e("DELETE /containers/{id}", s.removeContainer)
	e("GET /containers/{id}/spec", s.containerSpec)
	e("POST /containers/{id}/recreate", s.recreateContainer)
	e("POST /containers/{id}/duplicate", s.duplicateContainer)
	e("POST /containers/{id}/update", s.updateContainer)
	e("POST /containers/{id}/rename", s.renameContainer)
	e("POST /containers/{id}/action/{action}", s.containerAction)
	e("GET /containers/{id}/logs", s.containerLogs)
	e("GET /containers/{id}/stats", s.containerStats)
	e("GET /containers/{id}/top", s.containerTop)
	e("GET /containers/{id}/exec", s.containerExec) // websocket
	e("GET /containers/{id}/files", s.containerFiles)
	e("GET /containers/{id}/download", s.containerDownload)
	e("POST /containers/{id}/commit", s.commitContainer)

	e("GET /images", s.listImages)
	e("GET /images/{id...}", s.inspectImage)
	e("POST /images/pull", s.pullImage)
	e("POST /images/push", s.pushImage)
	e("POST /images/tag", s.tagImage)
	e("POST /images/build", s.buildImage)
	e("POST /images/prune", s.pruneImages)
	e("DELETE /images/{id...}", s.removeImage)

	e("GET /networks", s.listNetworks)
	e("POST /networks", s.createNetwork)
	e("GET /networks/{id}", s.inspectNetwork)
	e("DELETE /networks/{id}", s.removeNetwork)
	e("POST /networks/{id}/connect", s.connectNetwork)
	e("POST /networks/{id}/disconnect", s.disconnectNetwork)
	e("POST /networks/prune", s.pruneNetworks)

	e("GET /volumes", s.listVolumes)
	e("POST /volumes", s.createVolume)
	e("GET /volumes/{name}", s.inspectVolume)
	e("DELETE /volumes/{name}", s.removeVolume)
	e("POST /volumes/prune", s.pruneVolumes)

	e("GET /jump", s.jump)
	e("POST /tunnels", s.openTunnel)
	e("POST /exposures", s.createExposure)

	e("GET /stacks", s.listStacks)
	e("POST /stacks", s.createStack)
	e("GET /stacks/{name}", s.getStack)
	e("PUT /stacks/{name}", s.updateStack)
	e("DELETE /stacks/{name}", s.deleteStack)
	e("POST /stacks/{name}/action/{action}", s.stackAction)
	e("POST /stacks/{name}/adopt", s.adoptStack)
	e("GET /stacks/{name}/logs", s.stackLogs)
	e("GET /stacks/{name}/merged", s.stackMerged)
	e("POST /stacks/{name}/merge", s.stackMerge)
	e("PUT /stacks/{name}/location", s.stackLocation)

	e("GET /swarm", s.swarmInfo)
	e("GET /swarm/tokens", s.swarmTokens)
	e("POST /swarm/init", s.swarmInit)
	e("POST /swarm/join", s.swarmJoin)
	e("POST /swarm/leave", s.swarmLeave)
	e("POST /swarm/tokens/{which}/rotate", s.swarmRotate)
	e("GET /swarm/stacks", s.listSwarmStacks)
	e("POST /swarm/stacks", s.createSwarmStack)
	e("GET /swarm/stacks/{name}", s.getSwarmStack)
	e("PUT /swarm/stacks/{name}", s.updateSwarmStack)
	e("DELETE /swarm/stacks/{name}", s.removeSwarmStack)
	e("POST /swarm/stacks/{name}/deploy", s.deploySwarmStack)
	e("PUT /swarm/stacks/{name}/location", s.swarmStackLocation)
	e("GET /swarm/stacks/{name}/logs", s.swarmStackLogs)

	e("GET /nodes", s.listNodes)
	e("GET /nodes/{id}", s.inspectNode)
	e("POST /nodes/{id}/update", s.updateNode)
	e("DELETE /nodes/{id}", s.removeNode)

	e("GET /services", s.listServices)
	e("POST /services", s.createService)
	e("GET /services/{id}", s.inspectService)
	e("PUT /services/{id}", s.updateService)
	e("DELETE /services/{id}", s.removeService)
	e("POST /services/{id}/action/{action}", s.serviceAction)
	e("GET /services/{id}/logs", s.serviceLogs)

	for _, k := range []swarmObject{"secrets", "configs"} {
		e("GET /"+string(k), k.list(s))
		e("POST /"+string(k), k.create(s))
		e("GET /"+string(k)+"/{id}", k.inspect(s))
		e("DELETE /"+string(k)+"/{id}", k.remove(s))
	}
}

func cut(pattern string) (method, path string, ok bool) {
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == ' ' {
			return pattern[:i], pattern[i+1:], true
		}
	}
	return "", pattern, false
}
