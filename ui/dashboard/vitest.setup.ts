// React reads the act() flag from the global scope, not from process.env.
// Setting only process.env leaves every act() call warning that the
// environment is not configured.
(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
