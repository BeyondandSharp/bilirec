// SPDX-License-Identifier: GPL-3.0-only

package logging

import (
	"strings"
	"sync/atomic"

	"github.com/bilirec/bilirec/internal/modules/configfile"
)

var activeSourceSet atomic.Pointer[sourceLoggerSet]

func activateSourceSet(set *sourceLoggerSet) {
	activeSourceSet.Store(set)
}

func parseLogGroups(raw string) []string {
	out := make([]string, 0, len(logGroupNames))
	seen := make(map[string]struct{}, len(logGroupNames))
	for _, part := range strings.Split(raw, ",") {
		group := strings.ToLower(strings.TrimSpace(part))
		if group == "" {
			continue
		}
		known := false
		for _, candidate := range logGroupNames {
			if group == candidate {
				known = true
				break
			}
		}
		if !known {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		out = append(out, group)
	}
	return out
}

// BindConfigStore applies LOCAL_LOGS_GROUPS changes to the active source router
// without rebuilding sinks. It is a no-op when local split logging is disabled.
func BindConfigStore(store *configfile.Store) {
	if store == nil {
		return
	}
	store.OnConfigChange(func(change configfile.ConfigChange) {
		if change.Key != "LOCAL_LOGS_GROUPS" {
			return
		}
		set := activeSourceSet.Load()
		if set == nil {
			return
		}
		set.SetGroups(parseLogGroups(change.Value))
	})
}
