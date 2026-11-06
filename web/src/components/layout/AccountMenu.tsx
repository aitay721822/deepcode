import { KeyRoundIcon, LogOutIcon, ShieldIcon, UserIcon } from 'lucide-react'
import { useRef, useState } from 'react'
import { Link } from '../../lib/router'
import { Button } from '../ui/controls/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '../ui/overlay/dropdown-menu'

/** The signed-in user's menu. */
export function AccountMenu({ name, admin, onPassword, onSignOut }: { name: string; admin: boolean; onPassword: () => void; onSignOut: () => void }) {
  const [open, setOpen] = useState(false)
  // Run once the menu has closed and handed focus back to its button, so a dialog it opens returns focus there.
  const afterClose = useRef<() => void>(undefined)
  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="max-w-56 text-muted-foreground hover:text-foreground data-[state=open]:text-foreground" aria-label={`Account: ${name}`}>
          <UserIcon />
          <span className="hidden truncate sm:inline" dir="auto">{name}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52" onCloseAutoFocus={() => {
        afterClose.current?.()
        afterClose.current = undefined
      }}>
        <DropdownMenuItem onSelect={() => (afterClose.current = onPassword)}><KeyRoundIcon /> Change password</DropdownMenuItem>
        {admin && <DropdownMenuItem asChild><Link to="/admin"><ShieldIcon /> Admin console</Link></DropdownMenuItem>}
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onSignOut}><LogOutIcon /> Sign out</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
