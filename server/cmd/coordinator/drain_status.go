package main

import (
	"encoding/json"
	"net/http"
	"sort"

	coordinatormigration "github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/migration"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type zoneDrainStatus struct {
	ZoneID       string `json:"zone_id"`
	OwnerShards  int    `json:"owner_shards"`
	OpenTasks    int    `json:"open_tasks"`
	RunningTasks int    `json:"running_tasks"`
	OpenProgress int    `json:"open_progress"`
	Removable    bool   `json:"removable"`
}

func drainStatusHandler(routes *routing.Map, tasks coordinatormigration.TaskStore, progress coordinatormigration.ProgressBackend, configured map[string]struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if routes == nil || tasks == nil || progress == nil {
			http.Error(w, "drain status unavailable", http.StatusServiceUnavailable)
			return
		}
		open, err := tasks.LoadOpen(r.Context())
		if err != nil {
			http.Error(w, "load migration tasks", http.StatusServiceUnavailable)
			return
		}
		byID := make(map[string]*zoneDrainStatus, len(configured))
		for zoneID := range configured {
			byID[zoneID] = &zoneDrainStatus{ZoneID: zoneID}
		}
		for _, entry := range routes.Snapshot().Entries {
			if status := byID[entry.OwnerZoneID]; status != nil {
				status.OwnerShards++
			}
		}
		for _, task := range open {
			if status := byID[task.SourceZoneID]; status != nil {
				status.OpenTasks++
				if task.Status == coordinatormigration.StatusRunning {
					status.RunningTasks++
				}
			}
		}
		openProgress, err := progress.LoadOpenProgress(r.Context())
		if err != nil {
			http.Error(w, "load migration progress", http.StatusServiceUnavailable)
			return
		}
		for _, item := range openProgress {
			if status := byID[item.SourceZoneID]; status != nil {
				status.OpenProgress++
			}
		}
		result := make([]zoneDrainStatus, 0, len(byID))
		for _, status := range byID {
			status.Removable = status.OwnerShards == 0 && status.OpenTasks == 0 && status.OpenProgress == 0
			result = append(result, *status)
		}
		sort.Slice(result, func(i, j int) bool { return result[i].ZoneID < result[j].ZoneID })
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Zones []zoneDrainStatus `json:"zones"`
		}{Zones: result})
	}
}
