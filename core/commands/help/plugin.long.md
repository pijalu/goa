/plugin                         interactively list & toggle plugins (enter = enable/disable)
/plugin:list                    list installed plugins as text
/plugin:install:<git-url>       install a plugin from a git URL
/plugin:remove:<id>             uninstall a plugin
/plugin:enable:<id>             activate an installed plugin
/plugin:disable:<id>            deactivate an installed plugin

With no arguments, /plugin opens an interactive selector (like /config →
Tools): one row per installed plugin showing its on/off state. Press Enter on
a row to toggle that plugin enabled/disabled; the change is saved to the
plugin lockfile on disk immediately. Press Esc to close.

Note: Goa commands use the colon form (/plugin:disable:provider-quota), not
spaces. Tab completion after /plugin:enable: / /plugin:disable: offers only
the plugins in the matching state.

Plugins are installed under ~/.goa/plugins by default. Each plugin must
contain a plugin.yaml manifest with id, name, version, and entry fields.

Installation is git-based. Goa clones the repository, validates the manifest,
and records a SHA-256 content hash in the plugin lockfile. Installed plugins
start disabled; use /plugin:enable:<id> to activate them after reviewing the
code. Activation is permission-gated via the trust system: untrusted plugins
must be approved with /trust:<id> before they can be enabled.

Plugins may declare a skills_dir in their manifest. Skills in that directory
are loaded into the skill registry when the plugin is enabled.
