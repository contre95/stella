package devicesync

import (
	"context"
	"encoding/csv"
	"io"
)

// ExportCap mirrors stella-x4's MAX_ACTIVE_CARDS: the device loads at most
// this many cards per deck, so exporting more would only waste its SD space.
const ExportCap = 1000

// WriteDeckCSV streams the deck export as RFC 4180 CSV rows of
// "sync_id,front,back" — the same format the x4 device already parses, plus
// the leading ID column. Front/back are run through the service's Normalizer
// so each client family gets content it can actually render.
func (s *Service) WriteDeckCSV(ctx context.Context, userID, deckSyncID string, w io.Writer) error {
	deck, err := s.repo.GetDeckBySyncID(ctx, userID, deckSyncID)
	if err != nil {
		return err
	}
	rows, _, err := s.exportRows(ctx, userID, deck.ID)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	for _, r := range rows {
		front := s.normalize(r.Front, r.ImageFront != "")
		back := s.normalize(r.Back, r.ImageBack != "")
		if err := cw.Write([]string{r.SyncID, front, back}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
