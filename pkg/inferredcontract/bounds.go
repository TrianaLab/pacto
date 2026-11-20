package inferredcontract

// MaxInferredTags bounds the tags one repository may hold: past it the
// external is skipped with an error, never read in part.
const MaxInferredTags = 1000

// MaxInferredPublishesPerRun bounds the contracts one run pushes; the rest
// wait for the next run.
const MaxInferredPublishesPerRun = 20

// maxTagsVerified bounds how many of a repository's newest tags one run
// checks for this installation's signature. ponytail: a repository whose
// newest tags are all foreign publishes from the current surface alone;
// raise the bound if a registry ever needs more.
const maxTagsVerified = 10
