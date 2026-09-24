package ui

import (
	"fmt"
	"strings"

	"github.com/Pikaryu729/typesafe/internal/economy"
)

// earningsLabelWidth is the column the amounts line up against. Wide enough
// for the longest label the economy produces, so the figures form a column
// rather than a staircase.
const earningsLabelWidth = 16

// renderAward draws what an attempt paid and why.
//
// The breakdown is the point of the screen it appears on: a typist who is told
// only a total learns nothing about how to earn more, and one who can see that
// accuracy paid five bytes has a reason to slow down next time.
//
// An award worth nothing renders nothing at all. That covers three cases with
// no branching at the call sites — an anonymous session, a race nobody
// finished, and the moment before an award exists.
func renderAward(c *Context, a economy.Award) string {
	if a.Empty() {
		return ""
	}
	s := c.Styles

	var b strings.Builder
	for _, l := range a.Lines {
		b.WriteString(s.StatLabel.Render(fmt.Sprintf("%-*s", earningsLabelWidth, l.Label)))
		b.WriteString(s.Currency.Render(fmt.Sprintf("%+4d", l.Amount)))
		b.WriteString("\n")
	}

	b.WriteString(s.Help.Render(strings.Repeat("─", earningsLabelWidth+4)))
	b.WriteString("\n")
	b.WriteString(s.StatLabel.Render(fmt.Sprintf("%-*s", earningsLabelWidth, "total")))
	b.WriteString(s.Currency.Render(fmt.Sprintf("%+4d", a.Total)))
	b.WriteString(" ")
	b.WriteString(s.StatLabel.Render(economy.Unit))
	b.WriteString("\n\n")
	b.WriteString(s.StatLabel.Render(fmt.Sprintf("%-*s", earningsLabelWidth, "balance")))
	b.WriteString(s.Currency.Render(fmt.Sprintf("%4d", c.Balance)))
	b.WriteString(" ")
	b.WriteString(s.StatLabel.Render(economy.Unit))
	return b.String()
}

// formatBalance renders a balance for a header or a shop line.
func formatBalance(n int) string { return fmt.Sprintf("%d %s", n, economy.Unit) }
