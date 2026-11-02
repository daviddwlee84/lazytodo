package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"lazytodo/internal/config"
	"lazytodo/internal/model"
	"lazytodo/internal/service"
	"lazytodo/internal/skill"
	"lazytodo/internal/source"
	"lazytodo/internal/tui"
)

var Version = "dev"
var Commit = "unknown"

type envelope struct {
	SchemaVersion int `json:"schema_version"`
	Data          any `json:"data"`
}

func writeJSON(w io.Writer, v any) error {
	e := json.NewEncoder(w)
	e.SetEscapeHTML(false)
	return e.Encode(envelope{1, v})
}
func argsExactly(n int) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error {
		if e := cobra.ExactArgs(n)(c, args); e != nil {
			return service.Fail("usage", e.Error())
		}
		return nil
	}
}

func NewCommand() *cobra.Command {
	var configPath, color, interval string
	var jsonOutput bool
	root := &cobra.Command{Use: "lazytodo", Short: "A local-first, read-only inbox across work trackers", SilenceErrors: true, SilenceUsage: true, Args: argsExactly(0)}
	root.SetFlagErrorFunc(func(c *cobra.Command, e error) error { return service.Fail("usage", e.Error()) })
	root.PersistentFlags().StringVar(&configPath, "config", "", "Configuration file (default: XDG_CONFIG_HOME/lazytodo/config.toml)")
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Versioned JSON output; never prompt")
	root.PersistentFlags().StringVar(&color, "color", "auto", "Terminal color: auto, always, never")
	root.PersistentFlags().StringVar(&interval, "refresh-interval", "5m", "TUI refresh interval (0s disables automatic fetch)")
	initialize := func(c *cobra.Command) (*service.Service, error) {
		paths, e := config.ResolvePaths()
		if e != nil {
			return nil, service.Fail("config", e.Error())
		}
		selected, explicit, e := config.SelectPath(configPath, c.Flags().Changed("config"))
		if e != nil {
			return nil, service.Fail("config", e.Error())
		}
		paths.Config = selected
		var cp, ip *string
		if c.Flags().Changed("color") {
			cp = &color
		}
		if c.Flags().Changed("refresh-interval") {
			ip = &interval
		}
		cfg, e := config.LoadWithOverrides(paths.Config, explicit, cp, ip)
		if e != nil {
			return nil, service.Fail("config", e.Error())
		}
		return service.New(cfg, paths, source.New()), nil
	}
	terminal := func(c *cobra.Command) bool {
		in, iok := c.InOrStdin().(*os.File)
		out, ook := c.OutOrStdout().(*os.File)
		return iok && ook && term.IsTerminal(in.Fd()) && term.IsTerminal(out.Fd())
	}
	runTUI := func(c *cobra.Command, _ []string) error {
		if jsonOutput || !terminal(c) {
			return service.Fail("usage", "TUI requires input/output terminals and cannot be combined with --json")
		}
		svc, e := initialize(c)
		if e != nil {
			return e
		}
		return tui.Run(c.Context(), svc)
	}
	root.RunE = func(c *cobra.Command, args []string) error {
		if jsonOutput {
			return service.Fail("usage", "choose a data command, for example: lazytodo query --json")
		}
		if !terminal(c) {
			return c.Help()
		}
		return runTUI(c, args)
	}
	root.AddCommand(&cobra.Command{Use: "tui", Short: "Open the keyboard-first inbox", Args: argsExactly(0), RunE: runTUI})

	var limit, offset int
	var revision, viewName string
	query := &cobra.Command{Use: "query [search]", Short: "Search cached items without fetching sources", Args: func(c *cobra.Command, a []string) error {
		if len(a) > 1 {
			return service.Fail("usage", "quote the complete query as one argument")
		}
		return nil
	}}
	query.Flags().IntVar(&limit, "limit", 50, "Maximum returned items (1..1000)")
	query.Flags().IntVar(&offset, "offset", 0, "Result offset")
	query.Flags().StringVar(&revision, "revision", "", "Require a previous query revision when paging")
	query.Flags().StringVar(&viewName, "view", "", "Saved view (Open, All, Pinned, or configured name)")
	query.RunE = func(c *cobra.Command, a []string) error {
		svc, e := initialize(c)
		if e != nil {
			return e
		}
		text := ""
		if len(a) > 0 {
			text = a[0]
		}
		if viewName != "" {
			base := ""
			found := viewName == "All"
			if viewName == "Open" {
				base = "state:open"
				found = true
			}
			if viewName == "Pinned" {
				base = "pinned:true"
				found = true
			}
			for _, v := range svc.Config.Views {
				if v.Name == viewName {
					base = v.Query
					found = true
				}
			}
			if !found {
				return service.Fail("usage", "unknown view: "+viewName)
			}
			text = strings.TrimSpace(base + " " + text)
		}
		r, e := svc.Query(c.Context(), service.QueryRequest{Text: text, Limit: limit, Offset: offset, Revision: revision})
		if e != nil {
			return e
		}
		if jsonOutput {
			return writeJSON(c.OutOrStdout(), r)
		}
		w := tabwriter.NewWriter(c.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "PIN\tSOURCE\tITEM\tSTATE\tTITLE")
		for _, i := range r.Items {
			pin := ""
			if i.Pinned {
				pin = "*"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", pin, oneLine(i.SourceID), oneLine(i.DisplayRef), i.State, oneLine(i.Title))
		}
		if e = w.Flush(); e != nil {
			return e
		}
		fmt.Fprintf(c.OutOrStdout(), "%d/%d items · offset %d · cached only\n", r.Returned, r.Total, r.Offset)
		for _, st := range r.Sources {
			fmt.Fprintf(c.ErrOrStderr(), "%s: %s (%d cached)\n", st.ID, st.State, st.Count)
		}
		return nil
	}
	root.AddCommand(query)
	root.AddCommand(&cobra.Command{Use: "show <ref>", Short: "Show a cached item and its provenance", Args: argsExactly(1), RunE: func(c *cobra.Command, a []string) error {
		svc, e := initialize(c)
		if e != nil {
			return e
		}
		i, e := svc.Show(c.Context(), a[0])
		if e != nil {
			return e
		}
		statuses, e := svc.Sources(c.Context())
		if e != nil {
			return e
		}
		if jsonOutput {
			return writeJSON(c.OutOrStdout(), struct {
				Item    model.Item           `json:"item"`
				Sources []model.SourceStatus `json:"sources"`
			}{i, statuses})
		}
		fmt.Fprintf(c.OutOrStdout(), "%s\n%s · %s · %s\n%s\n\n%s\n\nref: %s\navailable: %t\n", oneLine(i.Title), oneLine(i.DisplayRef), i.State, oneLine(i.NativeState), oneLine(i.URL), model.Plain(i.Body), i.Ref, i.Available)
		return nil
	}})
	sourcesCommand := &cobra.Command{Use: "sources", Short: "Show cached coverage; explicitly discover or register sources with subcommands", Args: argsExactly(0), RunE: func(c *cobra.Command, a []string) error {
		svc, e := initialize(c)
		if e != nil {
			return e
		}
		r, e := svc.Sources(c.Context())
		if e != nil {
			return e
		}
		if jsonOutput {
			return writeJSON(c.OutOrStdout(), r)
		}
		if len(r) == 0 {
			fmt.Fprintln(c.OutOrStdout(), "No configured sources. Run lazytodo init to select sources, or lazytodo config edit.")
		}
		for _, st := range r {
			fmt.Fprintf(c.OutOrStdout(), "%s · %s · %s · %d items\n", st.ID, oneLine(st.Scope), st.State, st.Count)
			if st.Error != "" {
				fmt.Fprintf(c.OutOrStdout(), "  %s\n", oneLine(st.Error))
			}
		}
		return nil
	}}
	root.AddCommand(sourcesCommand)
	addSetupCommands(root, sourcesCommand, &configPath, &jsonOutput, terminal)
	var sourceID string
	fetch := &cobra.Command{Use: "fetch", Short: "Read configured sources and atomically refresh the local cache", Args: argsExactly(0), RunE: func(c *cobra.Command, a []string) error {
		svc, e := initialize(c)
		if e != nil {
			return e
		}
		r, fetchErr := svc.Fetch(c.Context(), sourceID, false)
		if jsonOutput {
			if e = writeJSON(c.OutOrStdout(), r); e != nil {
				return e
			}
		} else {
			for _, st := range r.Sources {
				fmt.Fprintf(c.OutOrStdout(), "%s: %s (%d items)\n", st.ID, st.State, st.Count)
				if st.Error != "" {
					fmt.Fprintln(c.ErrOrStderr(), oneLine(st.Error))
				}
			}
		}
		return fetchErr
	}}
	fetch.Flags().StringVar(&sourceID, "source", "", "Fetch only this configured source")
	root.AddCommand(fetch)
	pin := &cobra.Command{Use: "pin", Short: "Manage durable local pins (never source status)", Args: argsExactly(0)}
	for _, operation := range []string{"add", "remove"} {
		op := operation
		pin.AddCommand(&cobra.Command{Use: op + " <ref>", Short: op + " a local pin", Args: argsExactly(1), RunE: func(c *cobra.Command, a []string) error {
			svc, e := initialize(c)
			if e != nil {
				return e
			}
			if e = svc.Pin(c.Context(), a[0], op == "add"); e != nil {
				return e
			}
			if jsonOutput {
				return writeJSON(c.OutOrStdout(), map[string]any{"ref": a[0], "pinned": op == "add"})
			}
			fmt.Fprintf(c.OutOrStdout(), "Local pin %s: %s\n", op, a[0])
			return nil
		}})
	}
	root.AddCommand(pin)
	root.AddCommand(&cobra.Command{Use: "open <ref>", Short: "Explicitly open an item's native web page", Args: argsExactly(1), RunE: func(c *cobra.Command, a []string) error {
		if jsonOutput {
			return service.Fail("usage", "open is an external action, not a JSON data command")
		}
		svc, e := initialize(c)
		if e != nil {
			return e
		}
		return svc.Open(c.Context(), a[0])
	}})
	cfg := &cobra.Command{Use: "config", Short: "Inspect or explicitly edit local configuration", Args: argsExactly(0)}
	cfg.AddCommand(&cobra.Command{Use: "edit", Short: "Edit configuration using VISUAL, EDITOR, or vi; validate on return", Args: argsExactly(0), RunE: func(c *cobra.Command, _ []string) error {
		if jsonOutput || !terminal(c) {
			return service.Fail("usage", "config edit requires input/output terminals and cannot be combined with --json")
		}
		path, _, e := config.SelectPath(configPath, c.Flags().Changed("config"))
		if e != nil {
			return service.Fail("config", e.Error())
		}
		e = config.Edit(c.Context(), path, config.Editor(), c.InOrStdin(), c.OutOrStdout(), c.ErrOrStderr())
		var invalid *config.InvalidEdit
		if errors.As(e, &invalid) {
			return service.Fail("config", e.Error())
		}
		return e
	}})
	cfg.AddCommand(&cobra.Command{Use: "example", Short: "Print a static example; does not create files", Args: argsExactly(0), RunE: func(c *cobra.Command, a []string) error {
		if jsonOutput {
			return writeJSON(c.OutOrStdout(), map[string]string{"format": "toml", "content": config.Example})
		}
		_, e := fmt.Fprint(c.OutOrStdout(), config.Example)
		return e
	}})
	cfg.AddCommand(&cobra.Command{Use: "show", Short: "Show effective configuration and paths", Args: argsExactly(0), RunE: func(c *cobra.Command, a []string) error {
		svc, e := initialize(c)
		if e != nil {
			return e
		}
		if jsonOutput {
			return writeJSON(c.OutOrStdout(), struct {
				Config config.Config `json:"config"`
				Paths  config.Paths  `json:"paths"`
			}{svc.Config, svc.Paths})
		}
		b, e := json.MarshalIndent(struct {
			Config config.Config
			Paths  config.Paths
		}{svc.Config, svc.Paths}, "", "  ")
		if e == nil {
			_, e = fmt.Fprintln(c.OutOrStdout(), string(b))
		}
		return e
	}})
	root.AddCommand(cfg)
	sk := &cobra.Command{Use: "skill", Short: "Read the operational skill embedded in this binary", Args: argsExactly(0)}
	sk.AddCommand(&cobra.Command{Use: "print [overview|next]", Short: "Print static agent guidance, offline", Args: func(c *cobra.Command, a []string) error {
		if len(a) > 1 {
			return service.Fail("usage", "expected at most one skill topic")
		}
		return nil
	}, RunE: func(c *cobra.Command, a []string) error {
		topic := ""
		if len(a) > 0 {
			topic = a[0]
		}
		text, e := skill.Print(topic)
		if e != nil {
			return service.Fail("usage", e.Error())
		}
		if jsonOutput {
			return writeJSON(c.OutOrStdout(), map[string]string{"format": "markdown", "content": text})
		}
		_, e = fmt.Fprint(c.OutOrStdout(), text)
		return e
	}})
	root.AddCommand(sk)
	root.AddCommand(&cobra.Command{Use: "version", Short: "Show build identity", Args: argsExactly(0), RunE: func(c *cobra.Command, a []string) error {
		if jsonOutput {
			return writeJSON(c.OutOrStdout(), map[string]string{"version": Version, "commit": Commit})
		}
		_, e := fmt.Fprintf(c.OutOrStdout(), "lazytodo %s (%s)\n", Version, Commit)
		return e
	}})
	root.CompletionOptions.DisableDefaultCmd = true
	for _, group := range []*cobra.Command{pin, cfg, sk} {
		group.RunE = func(c *cobra.Command, _ []string) error {
			if jsonOutput {
				return service.Fail("usage", "choose a data subcommand; see --help")
			}
			return c.Help()
		}
	}
	return root
}

func oneLine(s string) string { return strings.Join(strings.Fields(model.Plain(s)), " ") }

func Execute(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	root := NewCommand()
	root.SetArgs(args)
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(errOut)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	code := "runtime"
	exit := 1
	message := model.Plain(err.Error())
	var typed *service.Error
	if errors.As(err, &typed) {
		code = typed.Code
		if code == "usage" || code == "config" {
			exit = 2
		}
	}
	if strings.HasPrefix(message, "unknown command ") {
		code = "usage"
		exit = 2
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		code = "cancelled"
		exit = 130
	}
	machine := false
	for _, arg := range args {
		if arg == "--json" {
			machine = true
		}
		if strings.HasPrefix(arg, "--json=") {
			v, e := strconv.ParseBool(strings.TrimPrefix(arg, "--json="))
			if e == nil {
				machine = v
			}
		}
	}
	if machine {
		_ = json.NewEncoder(errOut).Encode(map[string]any{"schema_version": 1, "error": map[string]string{"code": code, "message": message}})
	} else {
		fmt.Fprintf(errOut, "Error: %s\n", message)
	}
	return exit
}
