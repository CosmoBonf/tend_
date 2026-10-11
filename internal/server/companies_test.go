//go:build unix

package server

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/auth-com-br/tend/internal/proto"
	"github.com/auth-com-br/tend/internal/session"
)

// TestCompaniesAreSentToClientsAndSurviveARestart: a company made, filled,
// renamed and emptied over the company methods is what every client is
// sent in the snapshot, and it comes back when the server is stopped and
// started on the same state file. If it regresses, the companies the user
// set up vanish with a restart, or a client never sees one made by
// another.
func TestCompaniesAreSentToClientsAndSurviveARestart(t *testing.T) {
	state := filepath.Join(t.TempDir(), "work.json")
	first := persistentServer(t, state)
	a, err := first.NewWorkspaceIn("a", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := first.NewWorkspaceIn("b", t.TempDir())
	for _, ws := range []session.WorkspaceID{a, b} {
		if _, _, err := first.NewTab(ws, "t", PaneSpec{Command: []string{"/bin/sh"}}); err != nil {
			t.Fatal(err)
		}
	}

	res, err := first.Company(proto.MethodCompanyCreate, proto.CompanyParams{Name: "Acme", Workspace: uint64(b)})
	if err != nil {
		t.Fatal(err)
	}
	acme := res.(proto.CompanyCreateResult).Company
	must := func(method string, p proto.CompanyParams) {
		t.Helper()
		if _, err := first.Company(method, p); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	must(proto.MethodCompanyAssign, proto.CompanyParams{Company: acme, Workspace: uint64(a)})
	must(proto.MethodCompanyRename, proto.CompanyParams{Company: acme, Name: "Acme Ltda"})
	res, _ = first.Company(proto.MethodCompanyCreate, proto.CompanyParams{Name: "Gone"})
	must(proto.MethodCompanyDelete, proto.CompanyParams{Company: res.(proto.CompanyCreateResult).Company})

	want := []proto.CompanyInfo{{ID: acme, Name: "Acme Ltda", Workspaces: []uint64{uint64(b), uint64(a)}}}
	if got := first.Snapshot().Companies; !companiesEqual(got, want) {
		t.Fatalf("snapshot companies = %+v, want %+v", got, want)
	}
	if _, err := first.Company(proto.MethodCompanyAssign, proto.CompanyParams{Company: 999, Workspace: uint64(a)}); !errors.Is(err, session.ErrNoSuchCompany) {
		t.Errorf("assigning to a company that does not exist: %v", err)
	}

	first.saveStructure()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := persistentServer(t, state)
	if got := second.Snapshot().Companies; !companiesEqual(got, want) {
		t.Errorf("after a restart companies = %+v, want %+v", got, want)
	}
}

func companiesEqual(a, b []proto.CompanyInfo) bool {
	return slices.EqualFunc(a, b, func(x, y proto.CompanyInfo) bool {
		return x.ID == y.ID && x.Name == y.Name && slices.Equal(x.Workspaces, y.Workspaces)
	})
}

// TestAGroupMovesToAnotherCompanyOverTheProtocol: group.move_company moves
// a group's spaces from one company to the other, every client is sent the
// result, and it lasts a restart. If it regresses, the menu's "move to
// company" does nothing, or the move is undone when the server restarts.
func TestAGroupMovesToAnotherCompanyOverTheProtocol(t *testing.T) {
	state := filepath.Join(t.TempDir(), "work.json")
	first := persistentServer(t, state)
	a, err := first.NewWorkspaceIn("a", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := first.NewWorkspaceIn("b", t.TempDir())
	for _, ws := range []session.WorkspaceID{a, b} {
		if _, _, err := first.NewTab(ws, "t", PaneSpec{Command: []string{"/bin/sh"}}); err != nil {
			t.Fatal(err)
		}
		if err := first.GroupWorkspace(ws, "client"); err != nil {
			t.Fatal(err)
		}
	}
	res, _ := first.Company(proto.MethodCompanyCreate, proto.CompanyParams{Name: "Acme", Workspace: uint64(a)})
	acme := res.(proto.CompanyCreateResult).Company
	if _, err := first.Company(proto.MethodCompanyAssign, proto.CompanyParams{Company: acme, Workspace: uint64(b)}); err != nil {
		t.Fatal(err)
	}
	res, _ = first.Company(proto.MethodCompanyCreate, proto.CompanyParams{Name: "Auth"})
	auth := res.(proto.CompanyCreateResult).Company

	if err := first.Group(proto.MethodGroupMoveCompany, proto.GroupParams{Group: "client", From: acme, To: auth}); err != nil {
		t.Fatal(err)
	}
	want := []proto.CompanyInfo{
		{ID: acme, Name: "Acme"},
		{ID: auth, Name: "Auth", Workspaces: []uint64{uint64(a), uint64(b)}},
	}
	if got := first.Snapshot().Companies; !companiesEqual(got, want) {
		t.Errorf("companies = %+v, want %+v", got, want)
	}

	first.saveStructure()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := persistentServer(t, state)
	if got := second.Snapshot().Companies; !companiesEqual(got, want) {
		t.Errorf("after a restart companies = %+v, want %+v", got, want)
	}
	for _, g := range second.Snapshot().Groups {
		if g.Name == "client" && !slices.Equal(g.Companies, []uint64{auth}) {
			t.Errorf("the group is listed under %v, want Auth only", g.Companies)
		}
	}
}
