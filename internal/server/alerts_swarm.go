package server

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"ducktainer/internal/config"
)

type swarmNodeInfo struct {
	ID          string `json:"ID"`
	Description struct {
		Hostname string `json:"Hostname"`
	} `json:"Description"`
	Status struct {
		State   string `json:"State"`
		Message string `json:"Message"`
	} `json:"Status"`
	ManagerStatus *struct {
		Reachability string `json:"Reachability"`
	} `json:"ManagerStatus"`
}

type swarmServiceInfo struct {
	ID   string `json:"ID"`
	Spec struct {
		Name string `json:"Name"`
		Mode struct {
			ReplicatedJob *struct{} `json:"ReplicatedJob"`
			GlobalJob     *struct{} `json:"GlobalJob"`
		} `json:"Mode"`
	} `json:"Spec"`
	ServiceStatus struct {
		RunningTasks int `json:"RunningTasks"`
		DesiredTasks int `json:"DesiredTasks"`
	} `json:"ServiceStatus"`
}

// keepActive marks the alerts raised through an endpoint as still current,
// so that not being able to read the cluster does not resolve them.
func (a *alerter) keepActive(endpoint string, live map[string]bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, al := range a.active {
		if al.Endpoint == endpoint && (al.Kind == "swarm_node" || al.Kind == "swarm_service") {
			live[key] = true
		}
	}
}

// checkSwarm watches the clusters reachable through a manager endpoint: nodes
// that are down or unreachable, and services with fewer running tasks than
// wanted (with the reason their tasks give). A cluster seen through several
// managers is read once.
func (a *alerter) checkSwarm(rules []compiledRule, eps []config.Endpoint, live map[string]bool) {
	var swr []compiledRule
	for _, r := range rules {
		if r.Kind == "swarm_node" || r.Kind == "swarm_service" {
			swr = append(swr, r)
		}
	}
	if len(swr) == 0 {
		return
	}
	seen := map[string]bool{} // cluster|rule
	for _, ep := range eps {
		var mine []compiledRule
		for _, r := range swr {
			if r.covers(ep.Name, "") {
				mine = append(mine, r)
			}
		}
		if len(mine) == 0 {
			continue
		}
		c, err := a.s.Client(ep.Name)
		if err != nil {
			a.keepActive(ep.Name, live)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		st, err := swarmState(ctx, c)
		if err != nil {
			cancel()
			a.keepActive(ep.Name, live)
			continue
		}
		if !st.ControlAvailable {
			cancel()
			continue // not a manager: it cannot answer for the cluster
		}
		var cl struct{ ID string }
		var nodes []swarmNodeInfo
		var svcs []swarmServiceInfo
		var tasks []rawTask
		err = c.Get(ctx, "/swarm", nil, &cl)
		if err == nil {
			err = c.Get(ctx, "/nodes", nil, &nodes)
		}
		if err == nil {
			err = c.Get(ctx, "/services", url.Values{"status": {"true"}}, &svcs)
		}
		if err == nil {
			err = c.Get(ctx, "/tasks", url.Values{"filters": {`{"desired-state":["running"]}`}}, &tasks)
		}
		cancel()
		if err != nil {
			a.keepActive(ep.Name, live)
			continue
		}
		names := map[string]string{}
		for _, n := range nodes {
			names[n.ID] = n.Description.Hostname
		}
		for _, r := range mine {
			if seen[cl.ID+"|"+r.ID] {
				a.keepActive(ep.Name, live) // a repeated view of the same cluster
				continue
			}
			seen[cl.ID+"|"+r.ID] = true
			if r.Kind == "swarm_node" {
				a.checkSwarmNodes(r, ep.Name, cl.ID, nodes, live)
			} else {
				a.checkSwarmServices(r, ep.Name, cl.ID, svcs, tasks, names, live)
			}
		}
	}
}

func (a *alerter) checkSwarmNodes(r compiledRule, ep, cluster string, nodes []swarmNodeInfo, live map[string]bool) {
	for _, n := range nodes {
		key := r.ID + "|" + cluster + "|node:" + n.ID
		live[key] = true
		name := n.Description.Hostname
		problem := ""
		switch {
		case n.Status.State != "ready":
			problem = fmt.Sprintf("node %s is %s", name, n.Status.State)
			if n.Status.Message != "" {
				problem += " (" + n.Status.Message + ")"
			}
		case n.ManagerStatus != nil && n.ManagerStatus.Reachability != "reachable":
			problem = fmt.Sprintf("manager %s is %s", name, n.ManagerStatus.Reachability)
		}
		a.track(r, ep, key, "node "+name, problem == "", problem)
	}
}

func (a *alerter) checkSwarmServices(r compiledRule, ep, cluster string, svcs []swarmServiceInfo, tasks []rawTask, nodeNames map[string]string, live map[string]bool) {
	bad := map[string][]string{} // service -> reasons
	for _, t := range tasks {
		switch t.Status.State {
		case "pending", "failed", "rejected", "orphaned":
			why := t.Status.Err
			if why == "" {
				why = t.Status.Message
			}
			label := fmt.Sprintf("slot %d", t.Slot)
			if t.Slot == 0 && nodeNames[t.NodeID] != "" {
				label = nodeNames[t.NodeID] // global service: one task per node
			}
			bad[t.ServiceID] = append(bad[t.ServiceID], fmt.Sprintf("%s %s: %s", label, t.Status.State, strings.TrimSpace(why)))
		}
	}
	for _, sv := range svcs {
		name := sv.Spec.Name
		if sv.Spec.Mode.ReplicatedJob != nil || sv.Spec.Mode.GlobalJob != nil || !r.covers(ep, name) {
			continue
		}
		key := r.ID + "|" + cluster + "|svc:" + sv.ID
		live[key] = true
		want, got := sv.ServiceStatus.DesiredTasks, sv.ServiceStatus.RunningTasks
		problem := ""
		if want > 0 && got < want {
			problem = fmt.Sprintf("service %s: %d of %d tasks running", name, got, want)
			reasons := bad[sv.ID]
			if len(reasons) > 3 {
				reasons = append(reasons[:3], fmt.Sprintf("and %d more", len(reasons)-3))
			}
			if len(reasons) > 0 {
				problem += " — " + strings.Join(reasons, "; ")
			}
		}
		a.track(r, ep, key, "service "+name, problem == "", problem)
	}
}
