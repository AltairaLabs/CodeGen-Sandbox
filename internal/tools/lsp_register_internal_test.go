package tools

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
)

type lspCaptureRegistrar struct {
	names []string
}

func (c *lspCaptureRegistrar) AddTool(tool mcp.Tool, _ mcpserver.ToolHandlerFunc) {
	c.names = append(c.names, tool.Name)
}

func TestRegisterLSPTools_RegistersAllThree(t *testing.T) {
	reg := &lspCaptureRegistrar{}
	RegisterLSPTools(reg, &Deps{})
	assert.ElementsMatch(t, []string{
		"find_definition",
		"find_references",
		"rename_symbol",
	}, reg.names)
}
