package specs

// prettyName returns a string consisting of resource's namespace and name.
// If the namespace is empty, it returns only the name.
func prettyName(namespace string, name string) string {
	if namespace == "" {
		return name
	}

	return namespace + "/" + name
}
