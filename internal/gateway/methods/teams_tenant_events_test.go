package methods

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

type recordBus struct {
	events []bus.Event
}

func (b *recordBus) Publish(e bus.Event)                          { b.events = append(b.events, e) }
func (b *recordBus) Broadcast(e bus.Event)                        { b.events = append(b.events, e) }
func (b *recordBus) Subscribe(name string, ch chan<- bus.Event)   {}
func (b *recordBus) Unsubscribe(name string, ch chan<- bus.Event) {}
func (b *recordBus) PublishOutbound(msg bus.OutboundMessage)      {}

func TestTaskBusEvent_IncludesTenantID(t *testing.T) {
	tenantID := uuid.New()
	ctx := store.WithTenantID(context.Background(), tenantID)

	evt := taskBusEvent(ctx, protocol.EventTeamTaskApproved, protocol.TeamTaskEventPayload{
		TaskID: "task-123",
	})

	if evt.TenantID != tenantID {
		t.Fatalf("taskBusEvent.TenantID = %v, want %v", evt.TenantID, tenantID)
	}
	if evt.Name != protocol.EventTeamTaskApproved {
		t.Fatalf("taskBusEvent.Name = %v, want %v", evt.Name, protocol.EventTeamTaskApproved)
	}
}
