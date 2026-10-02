# Sourced by the scripts that read a key from a file.

# Where a key is kept when no variable names its file.
secret_dir=${XDG_CONFIG_HOME:-$HOME/.config}/wisp

# secret_file PREFIX NAME VALUE DEFAULT HOWTO prints a key: from the file
# VALUE, the value of the variable NAME, when set; else from the file
# DEFAULT. When there is neither and a terminal, it says HOWTO get a key,
# asks for it without echoing it, and saves it to DEFAULT, readable only by
# the user. Messages start with PREFIX, and show VALUE only when it looks
# like a path: NAME may have been set to the key itself.
secret_file() {
    local prefix=$1 name=$2 f=${3/#\~/$HOME} default=$4 howto=$5 key # "~/..." quoted, which the shell left as is
    if [[ -n $f ]]; then
        if [[ -f $f && -r $f ]]; then
            cat "$f"
            return
        elif [[ $f != */* ]]; then
            echo "$prefix: $name must be the path of a file holding the key, not the key itself" >&2
        else
            echo "$prefix: $name is $f, which is not a readable file" >&2
        fi
        return 1
    fi
    if [[ -f $default && -r $default ]]; then
        cat "$default"
        return
    fi
    if ! : 2>/dev/null </dev/tty; then
        echo "$prefix: no key: save it in $default, or set $name to the file holding it" >&2
        return 1
    fi
    printf '%s\nIt is saved to %s, readable only by you; set %s to keep it elsewhere.\nKey (not shown; Enter alone cancels): ' \
        "$howto" "$default" "$name" >/dev/tty
    read -rs key </dev/tty || true
    echo >/dev/tty
    [[ -n $key ]] || { echo "$prefix: no key given" >&2; return 1; }
    mkdir -p -m 700 "${default%/*}"
    (umask 077 && printf '%s' "$key" > "$default")
    printf '%s' "$key"
}
