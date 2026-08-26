import { setupServer } from 'msw/node';

// Empty server; per-test handlers are registered via server.use(...) in
// the individual *.test.tsx files. Keeping handlers test-local makes it
// obvious which fixtures back which assertion.
export const server = setupServer();
