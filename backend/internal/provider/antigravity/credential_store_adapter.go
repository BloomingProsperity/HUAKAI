package antigravity

import "github.com/BloomingProsperity/HUAKAI/internal/credentialstore"

func NewRefresher(store *credentialstore.Store, opts ...Option) *Refresher {
	r := &Refresher{Store: store, requireAccountLease: true}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}
