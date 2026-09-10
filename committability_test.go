package gomutant

import "context"

// committability counts a store's merged view per layer — the tests'
// own tally over the store's layer judgment (LayerReasons, the faces'
// path).
func committability(ctx context.Context, s *Store) (repo, localOnly int, err error) {
	prior, err := s.Load(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, f := range prior {
		if layer, _ := s.LayerReasons(f); layer == "repo" {
			repo++
		} else {
			localOnly++
		}
	}
	return repo, localOnly, nil
}
