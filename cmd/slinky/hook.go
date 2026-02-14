package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func hookCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hook [bash|zsh|fish]",
		Short: "Print shell hook code for eval integration",
		Long: `Print shell code that integrates slinky with your shell.

Add one of the following to your shell's rc file:

  # bash (~/.bashrc)
  eval "$(slinky config hook bash)"

  # zsh (~/.zshrc)
  eval "$(slinky config hook zsh)"

  # fish (~/.config/fish/config.fish)
  slinky config hook fish | source

If the shell argument is omitted, the shell is auto-detected from
the parent process.`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish"},
		RunE: func(cmd *cobra.Command, args []string) error {
			shell := ""
			if len(args) > 0 {
				shell = args[0]
			}
			if shell == "" {
				var err error
				shell, err = detectShell()
				if err != nil {
					return fmt.Errorf("cannot detect shell: %w\nSpecify the shell explicitly: slinky config hook bash", err)
				}
			}

			switch shell {
			case "bash":
				fmt.Print(bashHook)
			case "zsh":
				fmt.Print(zshHook)
			case "fish":
				fmt.Print(fishHook)
			default:
				return fmt.Errorf("unsupported shell %q: supported shells are bash, zsh, fish", shell)
			}
			return nil
		},
	}
}

const bashHook = `__slinky_hook() {
  if [ "${PWD}" != "${__SLINKY_DIR:-}" ]; then
    __SLINKY_DIR="${PWD}"
    slinky activate --hook
  fi
}
if [[ ! "${PROMPT_COMMAND:-}" =~ __slinky_hook ]]; then
  PROMPT_COMMAND="__slinky_hook${PROMPT_COMMAND:+;${PROMPT_COMMAND}}"
fi
trap 'slinky deactivate --hook 2>/dev/null' EXIT
__slinky_hook
`

const zshHook = `__slinky_hook() {
  slinky activate --hook
}
typeset -ag chpwd_functions
if [[ ! " ${chpwd_functions[*]} " =~ " __slinky_hook " ]]; then
  chpwd_functions+=(__slinky_hook)
fi
trap 'slinky deactivate --hook 2>/dev/null' EXIT
slinky activate --hook
`

const fishHook = `function __slinky_hook --on-variable PWD
  slinky activate --hook
end
function __slinky_exit --on-event fish_exit
  slinky deactivate --hook 2>/dev/null
end
slinky activate --hook
`

// detectShell identifies the parent process's shell.
// On Linux it reads /proc/$PPID/comm; on macOS it uses ps(1).
// Falls back to the SHELL environment variable.
func detectShell() (string, error) {
	ppid := os.Getppid()
	name := ""

	if runtime.GOOS == "linux" {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", ppid))
		if err == nil {
			name = strings.TrimSpace(string(data))
		}
	}

	if name == "" {
		out, err := exec.Command("ps", "-p", strconv.Itoa(ppid), "-o", "comm=").Output()
		if err == nil {
			name = strings.TrimSpace(string(out))
		}
	}

	if name != "" {
		name = filepath.Base(name)
		// Strip leading dash (login shells show as -bash, -zsh).
		name = strings.TrimPrefix(name, "-")
		switch name {
		case "bash", "zsh", "fish":
			return name, nil
		}
	}

	// Fall back to $SHELL.
	if envShell := os.Getenv("SHELL"); envShell != "" {
		base := filepath.Base(envShell)
		switch base {
		case "bash", "zsh", "fish":
			return base, nil
		}
	}

	return "", fmt.Errorf("could not detect shell from parent process (pid %d) or $SHELL", ppid)
}
