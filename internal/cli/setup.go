package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"lazytodo/internal/config"
	"lazytodo/internal/service"
	"lazytodo/internal/source"
	"lazytodo/internal/tui"
)

func setupTarget(provider, host string) (source.Target, error) {
	if provider != "github" && provider != "gitlab" {
		return source.Target{}, service.Fail("usage", "--provider must be github or gitlab")
	}
	c := config.Config{RefreshInterval: "5m", Color: "auto", Sources: []config.Source{{ID: "target", Kind: provider, Host: host, Scope: "check/project"}}}
	if e := c.Validate("."); e != nil {
		return source.Target{}, service.Fail("usage", e.Error())
	}
	return source.Target{Kind: provider, Host: c.Sources[0].Host}, nil
}

func addSetupCommands(root, sources *cobra.Command, pathFlag *string, jsonMode *bool, terminal func(*cobra.Command) bool) {
	draftFor := func(c *cobra.Command) (*config.SourceDraft, error) {
		path, _, e := config.SelectPath(*pathFlag, c.Flags().Changed("config"))
		if e != nil {
			return nil, service.Fail("config", e.Error())
		}
		draft, e := config.ReadSourceDraft(path)
		if e != nil {
			return nil, service.Fail("config", e.Error())
		}
		return draft, nil
	}
	root.AddCommand(&cobra.Command{Use: "init", Short: "Choose source hosts, discover recent repositories, and review local configuration", Args: argsExactly(0), RunE: func(c *cobra.Command, _ []string) error {
		if *jsonMode || !terminal(c) {
			return service.Fail("usage", "init requires input/output terminals and cannot use --json; use sources discover/add for scripted setup")
		}
		draft, e := draftFor(c)
		if e != nil {
			return e
		}
		// Presentation overrides must not be written into the raw file draft.
		if value, ok := os.LookupEnv("LAZYTODO_COLOR"); ok {
			draft.Config.Color = value
		}
		if c.Flags().Changed("color") {
			draft.Config.Color, _ = c.Flags().GetString("color")
		}
		if draft.Config.Color != "auto" && draft.Config.Color != "always" && draft.Config.Color != "never" {
			return service.Fail("usage", "color must be auto, always or never")
		}
		if c.Flags().Changed("mouse") {
			draft.Config.Mouse, _ = c.Flags().GetBool("mouse")
		}
		result, e := tui.RunSetup(c.Context(), draft, source.New())
		if e != nil {
			return e
		}
		if !result.Changed {
			fmt.Fprintln(c.OutOrStdout(), "Setup ended without configuration changes.")
			return nil
		}
		return printSourceSave(c, result, false)
	}})

	var discoverProvider, discoverHost string
	var limit int
	discover := &cobra.Command{Use: "discover", Short: "Explicitly list recent accessible repositories/projects using native credentials", Args: argsExactly(0), RunE: func(c *cobra.Command, _ []string) error {
		target, e := setupTarget(discoverProvider, discoverHost)
		if e != nil {
			return e
		}
		if limit < 1 || limit > 1000 {
			return service.Fail("usage", "--limit must be 1..1000")
		}
		result, discoveryErr := source.New().Discover(c.Context(), target, limit)
		if *jsonMode {
			if e = writeJSON(c.OutOrStdout(), result); e != nil {
				return e
			}
		} else {
			fmt.Fprintf(c.OutOrStdout(), "%s · %s · account %s\nOrder: %s (not issue update time)\n", target.Kind, target.Host, oneLine(result.Account), oneLine(result.Order))
			w := tabwriter.NewWriter(c.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "UPDATED/ACTIVITY\tVISIBILITY\tREPOSITORY / PROJECT")
			for _, candidate := range result.Candidates {
				visibility := "public"
				if candidate.Private {
					visibility = "restricted"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", candidate.UpdatedAt.Format("2006-01-02 15:04"), visibility, oneLine(candidate.Scope))
			}
			if e = w.Flush(); e != nil {
				return e
			}
			fmt.Fprintf(c.OutOrStdout(), "%d candidates from %d inspected records; no configuration written or issues fetched.\n", len(result.Candidates), result.Scanned)
			if result.Truncated {
				fmt.Fprintln(c.OutOrStdout(), "Partial/bounded discovery; more candidates may exist. Increase --limit (maximum 1000), or add a known scope manually.")
			}
		}
		if discoveryErr != nil {
			var typed *source.FetchError
			if errors.As(discoveryErr, &typed) {
				return service.Fail(typed.Code, typed.Message)
			}
			return discoveryErr
		}
		return nil
	}}
	discover.Flags().StringVar(&discoverProvider, "provider", "", "Required: github or gitlab")
	discover.Flags().StringVar(&discoverHost, "host", "", "Explicit host (default github.com or gitlab.com)")
	discover.Flags().IntVar(&limit, "limit", 200, "Maximum eligible candidates (1..1000)")
	sources.AddCommand(discover)

	var addProvider, addHost string
	var scopes []string
	add := &cobra.Command{Use: "add", Short: "Register explicit scopes locally without API probes or task fetches", Args: argsExactly(0), RunE: func(c *cobra.Command, _ []string) error {
		target, e := setupTarget(addProvider, addHost)
		if e != nil {
			return e
		}
		if len(scopes) == 0 {
			return service.Fail("usage", "provide at least one --scope owner/repository (repeat for additional scopes)")
		}
		additions := make([]config.Source, 0, len(scopes))
		for _, scope := range scopes {
			if strings.TrimSpace(scope) == "" {
				return service.Fail("usage", "--scope cannot be empty")
			}
			additions = append(additions, config.Source{Kind: target.Kind, Host: target.Host, Scope: scope})
		}
		// Validate caller-supplied values before touching the selected file.
		for n, s := range additions {
			s.ID = fmt.Sprintf("check-%d", n)
			check := config.Config{RefreshInterval: "5m", Color: "auto", Sources: []config.Source{s}}
			if e = check.Validate("."); e != nil {
				return service.Fail("usage", e.Error())
			}
		}
		draft, e := draftFor(c)
		if e != nil {
			return e
		}
		plan, e := draft.PlanSources(additions)
		if e != nil {
			return service.Fail("config", e.Error())
		}
		result, e := plan.Save(c.Context())
		if e != nil {
			if errors.Is(e, config.ErrConfigChanged) {
				return service.Fail("config_changed", e.Error())
			}
			return e
		}
		return printSourceSave(c, result, *jsonMode)
	}}
	add.Flags().StringVar(&addProvider, "provider", "", "Required: github or gitlab")
	add.Flags().StringVar(&addHost, "host", "", "Source host (default github.com or gitlab.com)")
	add.Flags().StringArrayVar(&scopes, "scope", nil, "Repository/project path; repeat to add several")
	sources.AddCommand(add)
}

func printSourceSave(c *cobra.Command, result config.SourceSave, jsonMode bool) error {
	if jsonMode {
		return writeJSON(c.OutOrStdout(), result)
	}
	fmt.Fprintf(c.OutOrStdout(), "Configuration: %q\nAdded %d sources; skipped %d already configured targets.\n", result.Path, len(result.Added), len(result.Skipped))
	for _, s := range result.Added {
		fmt.Fprintf(c.OutOrStdout(), "  %s · %s · %s\n", s.ID, s.Host, s.Scope)
	}
	fmt.Fprintf(c.OutOrStdout(), "No issues were fetched. Next: lazytodo --config %q fetch\nThen start lazytodo with the same config.\n", result.Path)
	return nil
}
