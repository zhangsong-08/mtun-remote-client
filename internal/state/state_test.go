package state

import (
	"testing"
	"time"

	"mtun/pkg/proto"
)

func TestPeersForFiltersByVisibility(t *testing.T) {
	reg := New("100.64.0.1", "100.64.0.0/10")
	reg.Upsert(Update{
		ID:  "owner",
		Now: time.Now(),
		Publish: []proto.PublishRule{
			{Name: "public", Listen: 10101, Visibility: proto.VisibleAll},
			{Name: "private", Listen: 10102, Visibility: proto.VisibleSelf},
			{Name: "shared", Listen: 10103, Visibility: proto.VisibleNodes, VisibleTo: []string{"viewer"}},
		},
	})
	reg.Upsert(Update{ID: "viewer", Now: time.Now()})

	peers := reg.PeersFor("viewer")
	if len(peers) != 1 || peers[0].NodeID != "owner" {
		t.Fatalf("peers = %#v, want only owner", peers)
	}
	got := make([]string, 0, len(peers[0].Rules))
	for _, r := range peers[0].Rules {
		got = append(got, r.Name)
	}
	want := []string{"public", "shared"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("visible rules = %v, want %v", got, want)
	}

	ownerPeers := reg.PeersFor("owner")
	if len(ownerPeers) != 1 || ownerPeers[0].NodeID != "viewer" || len(ownerPeers[0].Rules) != 0 {
		t.Fatalf("owner peers = %#v, want viewer without rules", ownerPeers)
	}
}

func TestSetRuleVisibility(t *testing.T) {
	reg := New("100.64.0.1", "100.64.0.0/10")
	reg.Upsert(Update{ID: "n1", Now: time.Now(), Publish: []proto.PublishRule{{Name: "web", Listen: 10101}}})

	if err := reg.SetRuleVisibility("n1", "web", proto.VisibleNodes, []string{"n2"}); err != nil {
		t.Fatalf("SetRuleVisibility: %v", err)
	}
	n, ok := reg.Get("n1")
	if !ok || n.Publish[0].Visibility != proto.VisibleNodes || len(n.Publish[0].VisibleTo) != 1 {
		t.Fatalf("node = %#v, want nodes/visible_to updated", n)
	}
	if err := reg.SetRuleVisibility("n1", "missing", proto.VisibleAll, nil); err == nil {
		t.Fatal("expected error for missing rule")
	}
}
