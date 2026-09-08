package cmd

import (
	"fmt"
	"strings"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/i18n"
	"github.com/spf13/cobra"
)

func newLanguageCmd() *cobra.Command {
	return &cobra.Command{
		Use: "language [code]", Short: "List interface languages or save a language preference",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: i18n.Codes(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				for _, language := range i18n.Languages() {
					fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", language.Code, language.Name)
				}
				return nil
			}
			code, ok := i18n.Parse(args[0])
			if !ok {
				return fmt.Errorf("unsupported language %q; choose %s", args[0], strings.Join(i18n.Codes(), ", "))
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			cfg.Lang = string(code)
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Language: %s. Reopen ccmux to apply; Settings switches an open TUI immediately.\n", code)
			return nil
		},
	}
}
func newContributeCmd() *cobra.Command {
	return &cobra.Command{
		Use: "contribute", Short: "Show how to report issues, improve translations, and submit pull requests",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			previous := i18n.Current()
			i18n.SetLanguage(cfg.Lang)
			defer i18n.SetLanguage(string(previous))
			fmt.Fprintln(cmd.OutOrStdout(), i18n.T("Report issues, improve translations, or submit a PR."))
			fmt.Fprintln(cmd.OutOrStdout(), "https://github.com/skzv/ccmux/blob/main/CONTRIBUTING.md")
			fmt.Fprintln(cmd.OutOrStdout(), "https://github.com/skzv/ccmux/issues")
			fmt.Fprintln(cmd.OutOrStdout(), "https://github.com/skzv/ccmux/pulls")
			return nil
		},
	}
}
