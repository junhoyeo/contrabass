import { useEffect, useMemo, useState } from 'react'
import type { BoardIssue } from '../types'
import { formatDateTime, formatIssueState } from '../i18n/format'
import { zhCN } from '../i18n/messages'
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

function getStateLabel(state: string): string {
  return formatIssueState(state)
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
      setErrorMessage(zhCN.board.createError)
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
      setErrorMessage(zhCN.board.updateError(identifier))
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
    <section className="board-view" aria-label={zhCN.board.ariaLabel}>
      <form className="board-view__create" onSubmit={handleCreateIssue}>
        <input
          className="board-view__input"
          type="text"
          placeholder={zhCN.board.titlePlaceholder}
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          aria-label={zhCN.board.titlePlaceholder}
          disabled={submitting}
        />
        <input
          className="board-view__input"
          type="text"
          placeholder={zhCN.board.descriptionPlaceholder}
          value={description}
          onChange={(event) => setDescription(event.target.value)}
          aria-label={zhCN.board.descriptionPlaceholder}
          disabled={submitting}
        />
        <button className="board-view__button" type="submit" disabled={submitting || !title.trim()}>
          {submitting ? zhCN.board.creating : zhCN.board.create}
        </button>
      </form>

      {errorMessage ? (
        <p className="board-view__error" role="alert">
          {errorMessage}
        </p>
      ) : null}

      {sortedIssues.length === 0 ? (
        <div className="board-view__empty">{zhCN.board.empty}</div>
      ) : (
        <div className="board-view__wrapper">
          <table className="board-view__table" aria-label={zhCN.board.tableAriaLabel}>
            <thead>
              <tr>
                <th>{zhCN.board.headers.identifier}</th>
                <th>{zhCN.board.headers.title}</th>
                <th>{zhCN.board.headers.state}</th>
                <th>{zhCN.board.headers.assignee}</th>
                <th>{zhCN.board.headers.updated}</th>
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
                            aria-label={zhCN.board.editTitleAria(issue.identifier)}
                          />
                          <input
                            className="board-view__input board-view__input--compact"
                            type="text"
                            value={editDraft.description}
                            onChange={(event) =>
                              setEditDraft((prev) => ({ ...prev, description: event.target.value }))
                            }
                            aria-label={zhCN.board.editDescriptionAria(issue.identifier)}
                          />
                          <div className="board-view__actions">
                            <button
                              className="board-view__button board-view__button--small"
                              type="button"
                              onClick={() => {
                                void handleSaveEdit(issue.identifier)
                              }}
                            >
                              {zhCN.board.save}
                            </button>
                            <button
                              className="board-view__button board-view__button--ghost board-view__button--small"
                              type="button"
                              onClick={handleCancelEditing}
                            >
                              {zhCN.board.cancel}
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
                                {zhCN.board.stateAction(formatIssueState(nextState))}
                              </button>
                            )
                          })}
                        </div>
                      </div>
                    </td>
                    <td>{issue.assignee || '-'}</td>
                    <td>{formatDateTime(issue.updated_at)}</td>
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
