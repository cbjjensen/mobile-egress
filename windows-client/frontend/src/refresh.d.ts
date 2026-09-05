export function createRefreshController<T>(load: () => Promise<T>, apply: (value: T) => void): {
  pause(): void
  resume(): void
  run(): Promise<void>
}
