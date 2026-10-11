package mcp

import "github.com/auth-com-br/tend/internal/agentfile"

// The rules every write to an agent's file follows live in
// internal/agentfile, which the permissions manager writes through too;
// these are the names this package has always used for them.

type writeOpts = agentfile.Options

var errChanging = agentfile.ErrChanging

func editFile(path string, opts writeOpts, edit func([]byte) ([]byte, error)) error {
	return agentfile.Edit(path, opts, edit)
}

var (
	readFile    = agentfile.ReadFile
	writeAtomic = agentfile.WriteAtomic
)
