# Sourced (not run) by the e2e scripts before their first git command; the
# shell twin of internal/gittest.Isolate (B-526). Requires $WORK.
#
# The developer's git config must not decide the result. Pointing
# GIT_CONFIG_GLOBAL at an empty file is not enough on its own: inherited
# command-scope overrides (GIT_CONFIG_COUNT/KEY_n/VALUE_n,
# GIT_CONFIG_PARAMETERS) outrank it, and an inherited commit.gpgsign=true
# made the fixture commit fail.
if [ -n "${GIT_CONFIG_COUNT:-}" ]; then
  _i=0
  while [ "$_i" -lt "$GIT_CONFIG_COUNT" ] 2>/dev/null; do
    unset "GIT_CONFIG_KEY_$_i" "GIT_CONFIG_VALUE_$_i"
    _i=$((_i + 1))
  done
  unset _i
fi
unset GIT_CONFIG_COUNT GIT_CONFIG_PARAMETERS GIT_CONFIG GIT_CONFIG_SYSTEM \
  GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_OBJECT_DIRECTORY \
  GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_COMMON_DIR GIT_NAMESPACE \
  GIT_CEILING_DIRECTORIES GIT_TEMPLATE_DIR \
  GIT_AUTHOR_NAME GIT_AUTHOR_EMAIL GIT_AUTHOR_DATE \
  GIT_COMMITTER_NAME GIT_COMMITTER_EMAIL GIT_COMMITTER_DATE
export GIT_CONFIG_GLOBAL="$WORK/gitconfig" GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0
printf '[commit]\n\tgpgsign = false\n[tag]\n\tgpgsign = false\n' > "$GIT_CONFIG_GLOBAL"
