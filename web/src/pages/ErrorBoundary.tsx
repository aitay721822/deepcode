import { Component, type ReactNode } from 'react'
import { Notice } from '../components/layout/Notice'
import { Button } from '../components/ui/controls/button'

type Props = { at: string; children: ReactNode }

/**
 * Around the whole app: a render error shows this notice with a Reload instead of a blank page (React logs the error).
 * at is the address: going to another (Back, Forward) tries again there.
 */
export class ErrorBoundary extends Component<Props, { failed: boolean; at: string }> {
  state = { failed: false, at: this.props.at }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  static getDerivedStateFromProps(props: Props, state: { at: string }) {
    return props.at === state.at ? null : { failed: false, at: props.at }
  }

  render() {
    if (!this.state.failed) return this.props.children
    return (
      <main>
        <Notice title="Something went wrong">
          <p className="text-muted-foreground">This page could not be shown.</p>
          <Button variant="outline" onClick={() => location.reload()}>Reload</Button>
          {/* A plain link: a full load, in case the page fails again. */}
          <a href="/" className="mx-auto block w-fit text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">Back to repositories</a>
        </Notice>
      </main>
    )
  }
}
