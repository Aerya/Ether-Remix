package collections

// pseBuilt appelle la fonction enregistrée par SetBuiltHook, si elle existe,
// quand la reconstruction d'une collection se termine (appelée en defer).
func (s *Service) pseBuilt() {
	if h := s.builtHook.Load(); h != nil {
		(*h)()
	}
}

// SetBuiltHook enregistre une fonction appelée après chaque reconstruction
// réussie d'une collection (les caches dérivés de ses membres s'invalident).
func (s *Service) SetBuiltHook(fn func()) {
	if fn == nil {
		s.builtHook.Store(nil)
		return
	}
	s.builtHook.Store(&fn)
}
