package app

func identityKind(k Identity) string {
	if len(k.Private) > 0 {
		return "private"
	}
	return "unavailable"
}
