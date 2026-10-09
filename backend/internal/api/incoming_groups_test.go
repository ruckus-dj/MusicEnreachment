package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type incomingGroupsTestStore struct {
	state    persistence.IncomingGroupingState
	captures []persistence.IncomingGroupingCapture
	groups   []persistence.IncomingGroupingGroup
}

func (store *incomingGroupsTestStore) State(context.Context) (persistence.IncomingGroupingState, error) {
	return store.state, nil
}

func (store *incomingGroupsTestStore) CurrentCaptures(context.Context) ([]persistence.IncomingGroupingCapture, error) {
	return store.captures, nil
}

func (store *incomingGroupsTestStore) List(context.Context) ([]persistence.IncomingGroupingGroup, error) {
	return store.groups, nil
}

func (store *incomingGroupsTestStore) Refresh(ctx context.Context, compute persistence.IncomingGroupingCompute) error {
	groups, err := compute(store.captures, store.groups)
	if err == nil {
		store.groups = groups
		store.state.NeedsRefresh = false
	}
	return err
}

func (store *incomingGroupsTestStore) Confirm(_ context.Context, epoch int64, compute persistence.IncomingGroupingCompute) error {
	if epoch != store.state.Revision {
		return persistence.ErrIncomingGroupingConflict
	}
	groups, err := compute(store.captures, store.groups)
	if err != nil {
		return err
	}
	store.groups = groups
	store.state.Revision++
	return nil
}

func (store *incomingGroupsTestStore) snapshotState() any {
	return struct {
		State  persistence.IncomingGroupingState
		Groups []persistence.IncomingGroupingGroup
	}{store.state, store.groups}
}

func incomingGroupsRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, path, strings.NewReader(body)))
	return recorder
}

func incomingCaptures(ids ...uuid.UUID) []persistence.IncomingGroupingCapture {
	result := make([]persistence.IncomingGroupingCapture, len(ids))
	for i, id := range ids {
		analysis, work, root, location := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		date := "2020"
		if i == len(ids)-1 {
			date = "2021"
		}
		result[i] = persistence.IncomingGroupingCapture{VariantID: id, WorkID: work, CaptureAnalysisID: &analysis, RootID: root, LocationID: location, ConfiguredPath: "/music", InventoryPath: "/music", RelativePath: fmt.Sprintf("album/%s.flac", id), SizeBytes: 10, Mtime: time.Now(), Tags: json.RawMessage(fmt.Sprintf(`{"ALBUM":["A"],"ARTIST":["B"],"DATE":["%s"]}`, date)), Ready: true}
	}
	return result
}

func persistedIncoming(groups []service.IncomingGroup) []persistence.IncomingGroupingGroup {
	result := make([]persistence.IncomingGroupingGroup, len(groups))
	for i, group := range groups {
		result[i] = persistence.IncomingGroupingGroup{ID: group.ID, Revision: group.Revision, Manual: group.Manual, Members: group.Members, Diagnostics: json.RawMessage(`[]`)}
	}
	return result
}

func TestIncomingGroupsAPIStaleAndInvalidEdits(t *testing.T) {
	groups := service.NewIncomingGroups(&incomingGroupsTestStore{state: persistence.IncomingGroupingState{Revision: 4}})
	handler := HandlerWithDependencies(Dependencies{IncomingGroups: groups})

	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/incoming-groups", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET incoming groups status = %d, want 200: %s", get.Code, get.Body.String())
	}

	stale := httptest.NewRecorder()
	handler.ServeHTTP(stale, httptest.NewRequest(http.MethodPost, "/incoming-groups/confirm", strings.NewReader(`{"epoch":3,"base_revision":"old","draft_revision":"old","actions":[]}`)))
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale confirm status = %d, want 409: %s", stale.Code, stale.Body.String())
	}

	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/incoming-groups/preview", strings.NewReader(`{"epoch":4,"base_revision":"`+emptyIncomingGroupsRevision(t)+`","actions":[{"kind":"unknown"}]}`)))
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid action status = %d, want 422: %s", invalid.Code, invalid.Body.String())
	}
}

func TestIncomingGroupsPreviewDraftConfirmsAreStatelessAndFenced(t *testing.T) {
	first, second, third := uuid.New(), uuid.New(), uuid.New()
	auto := []service.IncomingGroup{
		{ID: uuid.New(), Members: []uuid.UUID{first, second}},
		{ID: uuid.New(), Members: []uuid.UUID{third}},
	}
	cases := []struct {
		name   string
		action func(*testing.T, incomingGroupsBody) map[string]any
	}{
		{"merge", func(t *testing.T, snapshot incomingGroupsBody) map[string]any {
			t.Helper()
			if len(snapshot.Groups) < 2 {
				t.Fatalf("merge requires at least two groups: %+v", snapshot.Groups)
			}
			return map[string]any{"kind": "merge", "group_ids": []uuid.UUID{snapshot.Groups[0].ID, snapshot.Groups[1].ID}}
		}},
		{"split", func(t *testing.T, snapshot incomingGroupsBody) map[string]any {
			group := incomingGroupsGroupWithAtLeastMembers(t, snapshot, 2)
			return map[string]any{"kind": "split", "group_id": group.ID, "member_ids": []uuid.UUID{group.Members[0]}}
		}},
		{"move", func(t *testing.T, snapshot incomingGroupsBody) map[string]any {
			group := incomingGroupsGroupWithAtLeastMembers(t, snapshot, 2)
			other := incomingGroupsOtherGroup(t, snapshot, group.ID)
			return map[string]any{"kind": "move", "member_ids": []uuid.UUID{other.Members[0]}, "target_group_id": group.ID}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &incomingGroupsTestStore{state: persistence.IncomingGroupingState{Revision: 4}}
			store.captures = incomingCaptures(first, second, third)
			// The fake's persisted groups are materialized by the initial GET just as a
			// production GET refreshes its derived view. The snapshot shape remains real.
			store.groups = persistedIncoming(auto)
			handler := HandlerWithDependencies(Dependencies{IncomingGroups: service.NewIncomingGroups(store)})
			initial := incomingGroupsRequest(t, handler, http.MethodGet, "/incoming-groups", "")
			if initial.Code != http.StatusOK {
				t.Fatalf("initial snapshot: %d %s", initial.Code, initial.Body.String())
			}
			var snapshot incomingGroupsBody
			if err := json.Unmarshal(initial.Body.Bytes(), &snapshot); err != nil {
				t.Fatal(err)
			}
			base := snapshot.Revision
			action := tc.action(t, snapshot)
			before := store.snapshotState()
			body, _ := json.Marshal(map[string]any{"epoch": snapshot.Epoch, "base_revision": base, "actions": []any{action}})
			preview := incomingGroupsRequest(t, handler, http.MethodPost, "/incoming-groups/preview", string(body))
			if preview.Code != http.StatusOK {
				t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
			}
			var draft incomingGroupsBody
			if err := json.Unmarshal(preview.Body.Bytes(), &draft); err != nil {
				t.Fatal(err)
			}
			if draft.DraftRevision == "" || draft.Revision == base {
				t.Fatalf("preview did not produce a distinct draft revision: %+v", draft)
			}
			if after := store.snapshotState(); !reflect.DeepEqual(before, after) {
				t.Fatalf("preview mutated persisted state: before=%+v after=%+v", before, after)
			}
			confirm := func(revision string) *httptest.ResponseRecorder {
				b, _ := json.Marshal(map[string]any{"epoch": snapshot.Epoch, "base_revision": base, "draft_revision": revision, "actions": []any{action}})
				return incomingGroupsRequest(t, handler, http.MethodPost, "/incoming-groups/confirm", string(b))
			}
			bad := confirm(fmt.Sprintf("%064x", 1))
			if bad.Code != http.StatusConflict {
				t.Fatalf("mutated draft revision status=%d: %s", bad.Code, bad.Body.String())
			}
			if after := store.snapshotState(); !reflect.DeepEqual(before, after) {
				t.Fatalf("rejected draft mutated state: %+v", after)
			}
			multiMemberGroup := incomingGroupsGroupWithAtLeastMembers(t, snapshot, 2)
			firstMultiMember := multiMemberGroup.Members[0]
			otherGroup := incomingGroupsOtherGroup(t, snapshot, multiMemberGroup.ID)
			modifiedAction := map[string]any{"kind": "split", "group_id": multiMemberGroup.ID, "member_ids": []uuid.UUID{firstMultiMember}}
			switch tc.name {
			case "split":
				modifiedAction = map[string]any{"kind": "merge", "group_ids": []uuid.UUID{snapshot.Groups[0].ID, snapshot.Groups[1].ID}}
			case "move":
				modifiedAction = map[string]any{"kind": "move", "member_ids": []uuid.UUID{firstMultiMember}, "target_group_id": otherGroup.ID}
			}
			modifiedBody, _ := json.Marshal(map[string]any{"epoch": snapshot.Epoch, "base_revision": base, "draft_revision": draft.DraftRevision, "actions": []any{modifiedAction}})
			modified := incomingGroupsRequest(t, handler, http.MethodPost, "/incoming-groups/confirm", string(modifiedBody))
			if modified.Code != http.StatusConflict {
				t.Fatalf("modified draft status=%d: %s", modified.Code, modified.Body.String())
			}
			if after := store.snapshotState(); !reflect.DeepEqual(before, after) {
				t.Fatalf("rejected modified draft mutated state: %+v", after)
			}
			ok := confirm(draft.DraftRevision)
			if ok.Code != http.StatusOK {
				t.Fatalf("confirm: %d %s", ok.Code, ok.Body.String())
			}
			if store.state.Revision != snapshot.Epoch+1 {
				t.Fatalf("epoch after confirm=%d", store.state.Revision)
			}
			intervening := confirm(draft.DraftRevision)
			if intervening.Code != http.StatusConflict {
				t.Fatalf("intervening confirmation status=%d: %s", intervening.Code, intervening.Body.String())
			}
			store.state.Revision++
			store.state.NeedsRefresh = true
			invalidated := confirm(draft.DraftRevision)
			if invalidated.Code != http.StatusConflict {
				t.Fatalf("inventory-invalidated confirmation status=%d: %s", invalidated.Code, invalidated.Body.String())
			}
		})
	}
}

func emptyIncomingGroupsRevision(t *testing.T) string {
	t.Helper()
	revision, err := service.IncomingGroupsRevision(nil)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func incomingGroupsGroupWithAtLeastMembers(t *testing.T, snapshot incomingGroupsBody, count int) incomingGroupResponse {
	t.Helper()
	for _, group := range snapshot.Groups {
		if len(group.Members) >= count {
			return group
		}
	}
	t.Fatalf("no incoming group has at least %d members: %+v", count, snapshot.Groups)
	return incomingGroupResponse{}
}

func incomingGroupsOtherGroup(t *testing.T, snapshot incomingGroupsBody, excluded uuid.UUID) incomingGroupResponse {
	t.Helper()
	for _, group := range snapshot.Groups {
		if group.ID != excluded && len(group.Members) > 0 {
			return group
		}
	}
	t.Fatalf("no non-empty group other than %s in snapshot: %+v", excluded, snapshot.Groups)
	return incomingGroupResponse{}
}
