#!/bin/sh
# tmon — one-line installer
#
#   curl -fsSL https://raw.githubusercontent.com/guillaumemeyer/tmon/main/install.sh | sh
#
# Pure POSIX sh. This installer prepares the tmux config for tmon: it adds
# the TPM plugin line
#
#   set -g @plugin 'guillaumemeyer/tmon'
#
# to ~/.tmux.conf at the right place when it is not there yet. It does not
# install TPM and it does not run tmux. Finish the install inside tmux by
# pressing prefix + I (TPM then clones tmon and loads it).
#
# Safe to re-run: the plugin line is added only once. Override for
# testing: TMUX_CONF.

set -eu

TMUX_CONF="${TMUX_CONF:-$HOME/.tmux.conf}"
PLUGIN_LINE="set -g @plugin 'guillaumemeyer/tmon'"

fail() {
  echo "tmon: $1" >&2
  exit 1
}

# Add the plugin line at the right place:
#   - before the TPM initializer line, when the config has one (the
#     initializer must stay at the bottom of the config), otherwise
#   - after the last existing `set -g @plugin` line, otherwise
#   - at the end of the config.
add_plugin_line() {
  tmp="$TMUX_CONF.tmon.$$"
  trap 'rm -f "$tmp"' HUP INT TERM EXIT
  awk -v line="$PLUGIN_LINE" '
    /tpm\/tpm/ && !init { init_line = NR; init = 1 }
    /^[[:space:]]*set(-option)?[[:space:]]+-g[[:space:]]+@plugin/ { last_plugin = NR }
    { rows[NR] = $0 }
    END {
      if (init) at = init_line
      else if (last_plugin) at = last_plugin + 1
      else at = NR + 1
      for (i = 1; i < at; i++) print rows[i]
      print line
      for (i = at; i <= NR; i++) print rows[i]
    }
  ' "$TMUX_CONF" > "$tmp" || fail "could not write $TMUX_CONF"
  mv "$tmp" "$TMUX_CONF"
  echo "tmon: added $PLUGIN_LINE to $TMUX_CONF"
}

main() {
  if grep -qs "guillaumemeyer/tmon" "$TMUX_CONF" 2>/dev/null; then
    echo "tmon: plugin line already present in $TMUX_CONF"
  else
    if [ ! -f "$TMUX_CONF" ]; then
      : > "$TMUX_CONF"
      echo "tmon: created $TMUX_CONF"
    fi
    add_plugin_line
  fi

  if grep -qs 'tpm/tpm' "$TMUX_CONF" 2>/dev/null; then
    echo "tmon: done. Inside tmux, press prefix + I to install and load tmon"
  else
    echo "tmon: done. $TMUX_CONF does not load TPM yet." >&2
    echo "tmon: add the TPM initializer (run '~/.tmux/plugins/tpm/tpm') at the" >&2
    echo "tmon: bottom of $TMUX_CONF, then inside tmux press prefix + I." >&2
  fi
}

main "$@"
