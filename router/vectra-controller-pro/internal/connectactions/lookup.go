package connectactions

// Lookup reads execution metadata within the authenticated router/owner scope.
// It never advances a state, archives records, or changes started to interrupted.
// Missing records are distinct from corruption: only found=false with err=nil
// allows a caller to recognize a legacy native job without Connect metadata.
func (j *Journal) Lookup(b Binding, actionID string) (Record, bool, error) {
	if !validReference(b.RouterID) || !validReference(b.OwnerRef) {
		return Record{}, false, ErrUnauthorized
	}
	if !idPattern.MatchString(actionID) {
		return Record{}, false, ErrInvalidPayload
	}
	var rec Record
	var found bool
	err := j.withData(func(data *journalData) (bool, error) {
		var err error
		rec, found, err = j.lookup(data, scopeKey(b, actionID))
		return false, err
	})
	if err != nil {
		return Record{}, false, err
	}
	return rec, found, nil
}
