package service

func cloneTagMap(tags map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(tags))
	for key, values := range tags {
		if values == nil {
			cloned[key] = nil
			continue
		}
		cloned[key] = make([]string, len(values))
		copy(cloned[key], values)
	}
	return cloned
}
