package main

import "slices"

type Set map[string]bool

func NewSet() Set {
	return make(Set)
}

func (set Set) Add(element string) {
	set[element] = true
}

func (set Set) Remove(element string) {
	delete(set, element)
}

func (set Set) Contains(element string) bool {
	return set[element]
}

func (set Set) Size() int {
	return len(set)
}

func (set Set) Elements() []string {
	elements := make([]string, 0, len(set))
	for element := range set {
		elements = append(elements, element)
	}
	slices.Sort(elements)
	return elements
}

func (set Set) Clear() {
	for k := range set {
		delete(set, k)
	}
}
