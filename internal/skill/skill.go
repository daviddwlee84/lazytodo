package skill

import (
	"embed"
	"fmt"
)

//go:embed lazytodo/SKILL.md lazytodo/references/next.md
var files embed.FS

func Print(topic string) (string, error) {
	path := "lazytodo/SKILL.md"
	switch topic {
	case "", "overview":
	case "next":
		path = "lazytodo/references/next.md"
	default:
		return "", fmt.Errorf("unknown skill topic %q (overview, next)", topic)
	}
	b, e := files.ReadFile(path)
	return string(b), e
}
