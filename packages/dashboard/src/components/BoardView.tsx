import { useEffect, useMemo, useState } from 'react'
import type { BoardIssue } from '../types'
import './BoardView.css'

interface BoardViewProps {
  issues: BoardIssue[]
}

type IssueState = 'open' | 'in_progress' | 'done'

interface EditableDraft {
  title: string
  description: string
}

function sortByUpdatedAtDesc(entries: BoardIssue[]): BoardIssue[] {
  return [...entries].sort(
    (a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime(),
  )
}

function formatUpdatedAt(value: string): string {
  const timestamp = Date.parse(value)
  if (Number.isNaN(timestamp)) {
    return '-'
  }

  return new Date(timestamp).toLocaleString()
}

function getStateLabel(state: string): string {
  if (state === 'in_progress') {
    return 'In Progress'
  }

  if (state === 'done') {
    return 'Done'
  }

  return 'Open'
}

function getStateClassName(state: string): string {
  if (state === 'in_progress') {
    return 'board-view__state-badge board-view__state-badge--in-progress'
  }

  if (state === 'done') {
    return 'board-view__state-badge board-view__state-badge--done'
  }

  return 'board-view__state-badge board-view__state-badge--open'
}

async function readIssueFromResponse(response: Response): Promise<BoardIssue | null> {
  const bodyText = await response.text()
  if (!bodyText) {
    return null
  }

  try {
    return JSON.parse(bodyText) as BoardIssue
  } catch {
    return null
  }
}

function toIssueState(value: string): IssueState {
  if (value === 'in_progress' || value === 'done') {
    return value
  }

  return 'open'
}

function updateIssueList(
  entries: BoardIssue[],
  identifier: string,
  patch: Partial<Pick<BoardIssue, 'title' | 'description' | 'state'>>,
): BoardIssue[] {
  return entries.map((entry) => {
    if (entry.identifier !== identifier) {
      return entry
    }

    return {
      ...entry,
      ...patch,
      updated_at: new Date().toISOString(),
    }
  })
}

export function BoardView({ issues }: BoardViewProps) {
  const [localIssues, setLocalIssues] = useState(() => sortByUpdatedAtDesc(issues))
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [editingIdentifier, setEditingIdentifier] = useState<string | null>(null)
  const [editDraft, setEditDraft] = useState<EditableDraft>({ title: '', description: '' })
  const [submitting, setSubmitting] = useState(false)
  const [errorMessage, setErrorMessage] = useState('')

  useEffect(() => {
    setLocalIssues(sortByUpdatedAtDesc(issues))
  }, [issues])

  const sortedIssues = useMemo(() => sortByUpdatedAtDesc(localIssues), [localIssues])

  async function handleCreateIssue(event: { preventDefault: () => void }) {
    event.preventDefault()

    const nextTitle = title.trim()
    const nextDescription = description.trim()
    if (!nextTitle) {
      return
    }

    setErrorMessage('')
    setSubmitting(true)

    try {
      const response = await fetch('/api/v1/board/issues', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ title: nextTitle, description: nextDescription }),
      })

      if (!response.ok) {
        throw new Error(`Failed to create issue (${response.status})`)
      }

      const createdIssue = await readIssueFromResponse(response)
      if (createdIssue) {
        setLocalIssues((prev) => [createdIssue, ...prev])
      }

      setTitle('')
      setDescription('')
    } catch {
      setErrorMessage('Unable to create board issue')
    } finally {
      setSubmitting(false)
    }
  }

  async function patchIssue(
    identifier: string,
    patch: Partial<Pick<BoardIssue, 'title' | 'description' | 'state'>>,
  ) {
    setErrorMessage('')

    try {
      const response = await fetch(`/api/v1/board/issues/${encodeURIComponent(identifier)}`, {
        method: 'PATCH',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(patch),
      })

      if (!response.ok) {
        throw new Error(`Failed to update issue (${response.status})`)
      }

      const updatedIssue = await readIssueFromResponse(response)
      if (updatedIssue) {
        setLocalIssues((prev) => prev.map((entry) => (entry.identifier === identifier ? updatedIssue : entry)))
        return
      }

      setLocalIssues((prev) => updateIssueList(prev, identifier, patch))
    } catch {
      setErrorMessage(`Unable to update issue ${identifier}`)
    }
  }

  function handleStartEditing(issue: BoardIssue) {
    setEditingIdentifier(issue.identifier)
    setEditDraft({ title: issue.title, description: issue.description })
  }

  function handleCancelEditing() {
    setEditingIdentifier(null)
    setEditDraft({ title: '', description: '' })
  }

  async function handleSaveEdit(identifier: string) {
    const nextTitle = editDraft.title.trim()
    const nextDescription = editDraft.description.trim()

    if (!nextTitle) {
      return
    }

    await patchIssue(identifier, {
      title: nextTitle,
      description: nextDescription,
    })

    setEditingIdentifier(null)
  }

  return (
    <section className="board-view" aria-label="Board issues">
      <form className="board-view__create" onSubmit={handleCreateIssue}>
        <input
          className="board-view__input"
          type="text"
          placeholder="Issue title"
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          aria-label="Issue title"
          disabled={submitting}
        />
        <input
          className="board-view__input"
          type="text"
          placeholder="Issue description"
          value={description}
          onChange={(event) => setDescription(event.target.value)}
          aria-label="Issue description"
          disabled={submitting}
        />
        <button className="board-view__button" type="submit" disabled={submitting || !title.trim()}>
          {submitting ? 'Creating...' : 'Create'}
        </button>
      </form>

      {errorMessage ? (
        <p className="board-view__error" role="alert">
          {errorMessage}
        </p>
      ) : null}

      {sortedIssues.length === 0 ? (
        <div className="board-view__empty">No board issues</div>
      ) : (
        <div className="board-view__wrapper">
          <table className="board-view__table" aria-label="Board issues table">
            <thead>
              <tr>
                <th>Identifier</th>
                <th>Title</th>
                <th>State</th>
                <th>Assignee</th>
                <th>Updated</th>
              </tr>
            </thead>
            <tbody>
              {sortedIssues.map((issue) => {
                const isEditing = editingIdentifier === issue.identifier

                return (
                  <tr key={issue.id}>
                    <td className="board-view__mono">{issue.identifier}</td>
                    <td>
                      {isEditing ? (
                        <div className="board-view__edit">
                          <input
                            className="board-view__input board-view__input--compact"
                            type="text"
                            value={editDraft.title}
                            onChange={(event) =>
                              setEditDraft((prev) => ({ ...prev, title: event.target.value }))
                            }
                            aria-label={`Edit title ${issue.identifier}`}
                          />
                          <input
                            className="board-view__input board-view__input--compact"
                            type="text"
                            value={editDraft.description}
                            onChange={(event) =>
                              setEditDraft((prev) => ({ ...prev, description: event.target.value }))
                            }
                            aria-label={`Edit description ${issue.identifier}`}
                          />
                          <div className="board-view__actions">
                            <button
                              className="board-view__button board-view__button--small"
                              type="button"
                              onClick={() => {
                                void handleSaveEdit(issue.identifier)
                              }}
                            >
                              Save
                            </button>
                            <button
                              className="board-view__button board-view__button--ghost board-view__button--small"
                              type="button"
                              onClick={handleCancelEditing}
                            >
                              Cancel
                            </button>
                          </div>
                        </div>
                      ) : (
                        <button
                          className="board-view__title"
                          type="button"
                          onClick={() => handleStartEditing(issue)}
                        >
                          {issue.title}
                        </button>
                      )}
                    </td>
                    <td>
                      <div className="board-view__state">
                        <span className={getStateClassName(issue.state)}>{getStateLabel(issue.state)}</span>
                        <div className="board-view__state-actions">
                          {(['open', 'in_progress', 'done'] as IssueState[]).map((nextState) => {
                            const active = toIssueState(issue.state) === nextState

                            return (
                              <button
                                key={nextState}
                                className={`board-view__button board-view__button--small ${active ? 'board-view__button--active' : ''}`}
                                type="button"
                                disabled={active}
                                onClick={() => {
                                  void patchIssue(issue.identifier, { state: nextState })
                                }}
                              >
                                {nextState === 'in_progress' ? 'in progress' : nextState}
                              </button>
                            )
                          })}
                        </div>
                      </div>
                    </td>
                    <td>{issue.assignee || '-'}</td>
                    <td>{formatUpdatedAt(issue.updated_at)}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

export default BoardView
