import { forwardRef } from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { Button as WesuiButton, buttonVariants as wesuiButtonVariants } from '@wesui/primitives'
import type { ButtonProps as WesuiButtonProps } from '@wesui/primitives'
import { cn } from '@wesui/lib/cn'

const vscodeVariants = cva('', {
  variants: {
    variant: {
      vscode: 'bg-[var(--vscode-button-background)] text-[var(--vscode-button-foreground)] hover:bg-[var(--vscode-button-hoverBackground)]',
      'vscode-secondary': 'bg-[var(--vscode-button-secondaryBackground)] text-[var(--vscode-button-secondaryForeground)] border border-[var(--vscode-button-secondaryBackground)] hover:bg-[var(--vscode-button-secondaryHoverBackground)]',
    },
  },
})

type VscodeVariant = 'vscode' | 'vscode-secondary'
type WesuiVariant = NonNullable<WesuiButtonProps['variant']>

interface ButtonProps
  extends Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, 'children'>,
    Omit<VariantProps<typeof wesuiButtonVariants>, 'variant'> {
  variant?: WesuiVariant | VscodeVariant | null
  children: React.ReactNode
}

const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ variant, size, className, children, ...props }, ref) => {
    if (variant === 'vscode' || variant === 'vscode-secondary') {
      return (
        <button
          ref={ref}
          className={cn(
            'inline-flex items-center justify-center gap-1.5 font-medium transition-all disabled:opacity-40 disabled:cursor-not-allowed',
            vscodeVariants({ variant }),
            size === 'xs' ? 'h-6 px-2 text-caption rounded' :
            size === 'sm' ? 'h-7 px-3 text-caption rounded' :
            size === 'lg' ? 'h-10 px-5 text-small rounded' :
            'h-8 px-4 text-small rounded',
            className,
          )}
          style={{ transitionDuration: 'var(--duration-fast)' }}
          {...props}
        >
          {children}
        </button>
      )
    }
    return (
      <WesuiButton ref={ref} variant={variant as WesuiVariant} size={size} className={className} {...props}>
        {children}
      </WesuiButton>
    )
  },
)

Button.displayName = 'Button'

export { Button }
