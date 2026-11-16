package keyring

import kr "github.com/zalando/go-keyring"

const service = "gws-cli"

// ErrNotFound is returned when no credential exists for the user.
var ErrNotFound = kr.ErrNotFound

func Set(user, secret string) error { return kr.Set(service, user, secret) }

func Get(user string) (string, error) { return kr.Get(service, user) }

func Delete(user string) error { return kr.Delete(service, user) }
