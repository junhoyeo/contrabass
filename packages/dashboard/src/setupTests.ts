import { GlobalWindow } from 'happy-dom'
import '@testing-library/jest-dom'

const windowInstance = new GlobalWindow()
const globalScope = globalThis as Record<string, unknown>
const windowRecord = windowInstance as unknown as Record<string, unknown>

for (const key of Object.getOwnPropertyNames(windowInstance)) {
  if (!(key in globalScope)) {
    globalScope[key] = windowRecord[key]
  }
}

globalScope.window = windowInstance
globalScope.document = windowInstance.document
globalScope.navigator = windowInstance.navigator
