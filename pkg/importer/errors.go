package importer

import "errors"

var errNoToken = errors.New("no GitHub token available: set GITHUB_TOKEN or configure GitHub App (GITHUB_APP_ID + GITHUB_APP_KEY_PATH)")
