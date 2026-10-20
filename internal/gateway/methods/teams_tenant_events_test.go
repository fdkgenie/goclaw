package methods

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/gateway"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
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

// ---- emit-site tenant tagging ----

type teamEventTeamStore struct {
	store.TeamStore
	team    *store.TeamData
	members []store.TeamMemberData
	created *store.TeamData
	updated bool
	deleted bool
}

func (s *teamEventTeamStore) CreateTeam(_ context.Context, team *store.TeamData) error {
	team.ID = uuid.New()
	s.team = team
	s.created = team
	return nil
}

func (s *teamEventTeamStore) GetTeam(_ context.Context, _ uuid.UUID) (*store.TeamData, error) {
	if s.team == nil {
		return nil, errors.New("team not found")
	}
	return s.team, nil
}

func (s *teamEventTeamStore) GetTeamForAgent(_ context.Context, _ uuid.UUID) (*store.TeamData, error) {
	return nil, nil
}

func (s *teamEventTeamStore) UpdateTeam(_ context.Context, _ uuid.UUID, _ map[string]any) error {
	s.updated = true
	return nil
}

func (s *teamEventTeamStore) DeleteTeam(_ context.Context, _ uuid.UUID) error {
	s.deleted = true
	return nil
}

func (s *teamEventTeamStore) AddMember(_ context.Context, _, _ uuid.UUID, _ string) error {
	return nil
}

func (s *teamEventTeamStore) RemoveMember(_ context.Context, _, _ uuid.UUID) error {
	return nil
}

func (s *teamEventTeamStore) ListMembers(_ context.Context, _ uuid.UUID) ([]store.TeamMemberData, error) {
	return s.members, nil
}

func (s *teamEventTeamStore) HasTeamAccess(_ context.Context, _ uuid.UUID, _ string) (bool, error) {
	return true, nil
}

type teamEventAgentStore struct {
	store.AgentStore
	byKey map[string]*store.AgentData
	byID  map[uuid.UUID]*store.AgentData
}

func (s *teamEventAgentStore) GetByKey(_ context.Context, key string) (*store.AgentData, error) {
	ag, ok := s.byKey[key]
	if !ok {
		return nil, errors.New("agent not found")
	}
	return ag, nil
}

func (s *teamEventAgentStore) GetByID(_ context.Context, id uuid.UUID) (*store.AgentData, error) {
	ag, ok := s.byID[id]
	if !ok {
		return nil, errors.New("agent not found")
	}
	return ag, nil
}

type teamEventLinkStore struct {
	store.AgentLinkStore
	link    *store.AgentLinkData
	updated map[string]any
	deleted bool
}

func (s *teamEventLinkStore) CreateLink(_ context.Context, link *store.AgentLinkData) error {
	link.ID = uuid.New()
	s.link = link
	return nil
}

func (s *teamEventLinkStore) GetLink(_ context.Context, _ uuid.UUID) (*store.AgentLinkData, error) {
	if s.link == nil {
		return nil, errors.New("link not found")
	}
	return s.link, nil
}

func (s *teamEventLinkStore) UpdateLink(_ context.Context, _ uuid.UUID, updates map[string]any) error {
	s.updated = updates
	return nil
}

func (s *teamEventLinkStore) DeleteLink(_ context.Context, _ uuid.UUID) error {
	s.deleted = true
	return nil
}

type tenantEventFixture struct {
	tenantID uuid.UUID
	ctx      context.Context
	client   *gateway.Client
	frames   <-chan []byte
	teams    *TeamsMethods
	links    *AgentLinksMethods

	mu     sync.Mutex
	events []bus.Event

	teamStore  *teamEventTeamStore
	agentStore *teamEventAgentStore
	linkStore  *teamEventLinkStore
	leadID     uuid.UUID
	memberID   uuid.UUID
	teamID     uuid.UUID
	linkID     uuid.UUID
}

func newTenantEventFixture(t *testing.T) *tenantEventFixture {
	t.Helper()
	tenantID := uuid.New()
	leadID, memberID, teamID, linkID := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	lead := &store.AgentData{BaseModel: store.BaseModel{ID: leadID}, AgentKey: "lead", DisplayName: "Lead", AgentType: store.AgentTypePredefined}
	member := &store.AgentData{BaseModel: store.BaseModel{ID: memberID}, AgentKey: "member", DisplayName: "Member", AgentType: store.AgentTypePredefined}
	agentStore := &teamEventAgentStore{
		byKey: map[string]*store.AgentData{"lead": lead, "member": member},
		byID:  map[uuid.UUID]*store.AgentData{leadID: lead, memberID: member},
	}
	teamStore := &teamEventTeamStore{
		team:    &store.TeamData{BaseModel: store.BaseModel{ID: teamID}, Name: "Alpha", LeadAgentID: leadID},
		members: []store.TeamMemberData{{TeamID: teamID, AgentID: leadID, AgentKey: "lead"}},
	}
	linkStore := &teamEventLinkStore{
		link: &store.AgentLinkData{
			BaseModel:         store.BaseModel{ID: linkID},
			SourceAgentID:     leadID,
			TargetAgentID:     memberID,
			SourceAgentKey:    "lead",
			TargetAgentKey:    "member",
			Direction:         store.LinkDirectionOutbound,
			Status:            store.LinkStatusActive,
			MaxConcurrent:     3,
			SourceDisplayName: "Lead",
		},
	}

	mb := bus.New()
	f := &tenantEventFixture{
		tenantID:   tenantID,
		ctx:        store.WithTenantID(context.Background(), tenantID),
		teamStore:  teamStore,
		agentStore: agentStore,
		linkStore:  linkStore,
		leadID:     leadID,
		memberID:   memberID,
		teamID:     teamID,
		linkID:     linkID,
	}
	f.client, f.frames = gateway.NewCapturingTestClient(permissions.RoleAdmin, tenantID, "admin-a", 4)
	f.teams = &TeamsMethods{teamStore: teamStore, agentStore: agentStore, msgBus: mb}
	f.links = &AgentLinksMethods{linkStore: linkStore, agentStore: agentStore, msgBus: mb}
	mb.Subscribe("test-capture", func(e bus.Event) {
		f.mu.Lock()
		f.events = append(f.events, e)
		f.mu.Unlock()
	})
	return f
}

func (f *tenantEventFixture) call(t *testing.T, handler func(context.Context, *gateway.Client, *protocol.RequestFrame), params map[string]any) {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	handler(f.ctx, f.client, &protocol.RequestFrame{ID: "1", Params: raw})
	select {
	case <-f.frames:
	default:
		t.Fatal("handler sent no response")
	}
}

func (f *tenantEventFixture) event(t *testing.T, name string) bus.Event {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.events {
		if e.Name == name {
			return e
		}
	}
	names := make([]string, 0, len(f.events))
	for _, e := range f.events {
		names = append(names, e.Name)
	}
	t.Fatalf("event %q not emitted; got %v", name, names)
	return bus.Event{}
}

func TestTeamLifecycleEmitSitesCarryHandlerTenant(t *testing.T) {
	tests := []struct {
		name  string
		event string
		call  func(f *tenantEventFixture)
	}{
		{
			name:  "team.created",
			event: protocol.EventTeamCreated,
			call: func(f *tenantEventFixture) {
				f.call(t, f.teams.handleCreate, map[string]any{
					"name": "Alpha", "lead": "lead", "members": []string{"lead"},
				})
			},
		},
		{
			name:  "team.updated",
			event: protocol.EventTeamUpdated,
			call: func(f *tenantEventFixture) {
				f.call(t, f.teams.handleUpdate, map[string]any{
					"teamId": f.teamID.String(), "name": "Renamed",
				})
			},
		},
		{
			name:  "team.deleted",
			event: protocol.EventTeamDeleted,
			call: func(f *tenantEventFixture) {
				f.call(t, f.teams.handleDelete, map[string]any{"teamId": f.teamID.String()})
			},
		},
		{
			name:  "team.member.added",
			event: protocol.EventTeamMemberAdded,
			call: func(f *tenantEventFixture) {
				f.call(t, f.teams.handleAddMember, map[string]any{
					"teamId": f.teamID.String(), "agent": "member",
				})
			},
		},
		{
			name:  "team.member.removed",
			event: protocol.EventTeamMemberRemoved,
			call: func(f *tenantEventFixture) {
				f.call(t, f.teams.handleRemoveMember, map[string]any{
					"teamId": f.teamID.String(), "agentId": f.memberID.String(),
				})
			},
		},
		{
			name:  "agent_link.created",
			event: protocol.EventAgentLinkCreated,
			call: func(f *tenantEventFixture) {
				f.call(t, f.links.handleCreate, map[string]any{
					"sourceAgent": "lead", "targetAgent": "member",
				})
			},
		},
		{
			name:  "agent_link.updated",
			event: protocol.EventAgentLinkUpdated,
			call: func(f *tenantEventFixture) {
				f.call(t, f.links.handleUpdate, map[string]any{
					"linkId": f.linkID.String(), "status": store.LinkStatusDisabled,
				})
			},
		},
		{
			name:  "agent_link.deleted",
			event: protocol.EventAgentLinkDeleted,
			call: func(f *tenantEventFixture) {
				f.call(t, f.links.handleDelete, map[string]any{"linkId": f.linkID.String()})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newTenantEventFixture(t)
			tc.call(f)

			evt := f.event(t, tc.event)
			if evt.TenantID != f.tenantID {
				t.Fatalf("%s TenantID = %v, want handler ctx tenant %v", tc.event, evt.TenantID, f.tenantID)
			}
		})
	}
}
