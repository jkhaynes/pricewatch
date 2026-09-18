package card

import "errors"

// Card-level: this card cannot be priced. Record it, skip it, carry on.
var (
	ErrUnsupportedVariant = errors.New("unsupported variant")
	ErrNotFound           = errors.New("card not found at source")
	ErrVariantUnavailable = errors.New("variant not priced at source")
	ErrVariantAmbiguous   = errors.New("variant matches more than one price at source")
)

// Provider-level: nothing can be priced right now. Stop dispatching; blame no card.
var (
	ErrRateLimited    = errors.New("rate limited by source")
	ErrQuotaExhausted = errors.New("daily request quota exhausted")
)
