export const apiConfig = {
  baseUrl: import.meta.env.VITE_API_BASE_URL || "http://localhost:8080",
  enableMock: String(import.meta.env.VITE_ENABLE_MOCK ?? "true") !== "false",
  defaultProvider: import.meta.env.VITE_DEFAULT_PROVIDER || "openai",
  defaultModel: import.meta.env.VITE_DEFAULT_MODEL || "gpt-5-mini",
};
