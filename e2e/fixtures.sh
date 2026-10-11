#!/bin/sh
# Creates bare repos under /srv/git/<org>/<name> for the e2e tests.
set -eu

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=e2e GIT_AUTHOR_EMAIL=e2e@example.test
export GIT_COMMITTER_NAME=e2e GIT_COMMITTER_EMAIL=e2e@example.test

rm -rf /srv/git/*

make_repo() {
	work=$(mktemp -d)
	git -C "$work" init -q -b main
	mkdir -p "$work/pkg"
	printf '# %s\n' "$1" > "$work/README.md"
	git -C "$work" add .
	git -C "$work" commit -q -m "initial commit"
	printf 'package pkg\n\nfunc Handle(err error) string {\n\treturn err.Error()\n}\n' > "$work/pkg/handler.go"
	git -C "$work" add .
	git -C "$work" commit -q -m "add handler"
	git -C "$work" tag v1.0.0

	git clone -q --bare "$work" "/srv/git/$1"
	git -C "/srv/git/$1" update-server-info
	rm -rf "$work"
}

make_repo org/repo-a
make_repo org/repo-b
make_repo other/secret
chmod -R a+rX /srv/git
