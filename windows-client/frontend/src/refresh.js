// One read at a time. A user action invalidates reads started before it.
export function createRefreshController(load, apply) {
  let active, activeRevision, revision = 0, paused = false
  return {
    pause() { paused = true; revision++ },
    resume() { paused = false },
    run() {
      if (paused) return Promise.resolve()
      if (active) return activeRevision === revision ? active : active.catch(() => {}).then(() => this.run())
      const started = revision
      activeRevision = started
      active = (async () => {
        try {
          const result = await load()
          if (!paused && started === revision) apply(result)
        } catch (error) {
          if (!paused && started === revision) throw error
        }
      })().finally(() => { active = undefined })
      return active
    },
  }
}
