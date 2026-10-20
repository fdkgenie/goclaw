package methods

import (
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func makeDashboardTasks(n int) []store.TeamTaskData {
	return make([]store.TeamTaskData, n)
}

func TestBuildTaskListResponse(t *testing.T) {
	cases := []struct {
		name        string
		taskCount   int
		wantCount   int
		wantLen     int
		wantHasMore bool
	}{
		{"exactly_limit", 200, 200, 200, false},
		{"over_limit", 205, 200, 200, true},
		{"empty", 0, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := buildTaskListResponse(makeDashboardTasks(tc.taskCount))
			tasks, ok := resp["tasks"].([]store.TeamTaskData)
			if !ok {
				t.Fatalf("tasks type = %T, want []store.TeamTaskData", resp["tasks"])
			}
			if len(tasks) != tc.wantLen {
				t.Fatalf("len(tasks) = %d, want %d", len(tasks), tc.wantLen)
			}
			if got := resp["count"].(int); got != tc.wantCount {
				t.Fatalf("count = %d, want %d", got, tc.wantCount)
			}
			if got := resp["hasMore"].(bool); got != tc.wantHasMore {
				t.Fatalf("hasMore = %v, want %v", got, tc.wantHasMore)
			}
		})
	}
}
