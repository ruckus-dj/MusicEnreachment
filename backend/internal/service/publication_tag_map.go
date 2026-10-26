package service

func cloneTagMap(tags map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(tags))
	for key, values := range tags {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}
