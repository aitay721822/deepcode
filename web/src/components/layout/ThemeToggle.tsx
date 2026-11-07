import { MoonIcon, SunIcon } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { ThemeAnimationType, useModeAnimation } from 'react-theme-switch-animation'
import { Button } from '../ui/controls/button'

// The hook reads and writes localStorage without a guard; where storage throws, index.html's dark default stays.
const storage = (() => {
  try {
    localStorage.getItem('theme')
    return true
  } catch {
    return false
  }
})()

export function ThemeToggle() {
  return storage ? <Toggle /> : null
}

/** Reveals the other theme in a circle from the button; the hook toggles .dark on <html> and stores the choice. */
function Toggle() {
  const { ref, toggleSwitchTheme, isDarkMode } = useModeAnimation({
    animationType: ThemeAnimationType.CIRCLE,
    duration: 600,
  })
  return (
    <Button
      ref={ref}
      variant="ghost"
      size="icon-sm"
      className="text-muted-foreground hover:text-foreground"
      aria-label={`Switch to ${isDarkMode ? 'light' : 'dark'} theme`}
      onClick={toggleSwitchTheme}
    >
      <AnimatePresence mode="wait" initial={false}>
        <motion.span
          key={isDarkMode ? 'sun' : 'moon'}
          className="grid"
          initial={{ opacity: 0, rotate: -90, scale: 0.6 }}
          animate={{ opacity: 1, rotate: 0, scale: 1 }}
          exit={{ opacity: 0, rotate: 90, scale: 0.6 }}
          transition={{ duration: 0.18 }}
        >
          {isDarkMode ? <SunIcon className="size-[18px]" /> : <MoonIcon className="size-[18px]" />}
        </motion.span>
      </AnimatePresence>
    </Button>
  )
}
