import * as AlertDialog from '@radix-ui/react-alert-dialog';
import * as Dialog from '@radix-ui/react-dialog';
import { clsx } from 'clsx';
import { X } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { Button, ErrorAlert } from './ui';

const overlay = 'fixed inset-0 z-40 bg-black/45 backdrop-blur-[1px]';
const content =
  'fixed top-[8vh] left-1/2 z-50 flex max-h-[84vh] w-[calc(100vw-32px)] -translate-x-1/2 flex-col rounded-lg border border-line bg-surface shadow-2xl focus:outline-none';

export function Modal({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  width = 'max-w-lg',
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  width?: string;
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className={overlay} />
        <Dialog.Content className={clsx(content, width)}>
          <div className="flex items-start gap-3 border-b border-line px-4 py-3">
            <div className="min-w-0 flex-1">
              <Dialog.Title className="text-[15px] font-semibold">{title}</Dialog.Title>
              {description ? (
                <Dialog.Description className="mt-0.5 text-[13px] text-muted">
                  {description}
                </Dialog.Description>
              ) : (
                <Dialog.Description className="sr-only">{title}</Dialog.Description>
              )}
            </div>
            <Dialog.Close asChild>
              <Button variant="ghost" size="sm" aria-label="Close" className="-mr-1 px-1.5">
                <X className="size-4" aria-hidden />
              </Button>
            </Dialog.Close>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4">{children}</div>
          {footer && (
            <div className="flex items-center justify-end gap-2 border-t border-line px-4 py-3">
              {footer}
            </div>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

/**
 * A confirmation dialog. `onConfirm` may return a promise; the dialog stays
 * open (showing a spinner, then any error) until it settles.
 */
export function Confirm({
  trigger,
  title,
  description,
  confirmLabel = 'Confirm',
  destructive,
  onConfirm,
  open: controlledOpen,
  onOpenChange,
}: {
  trigger?: ReactNode;
  title: ReactNode;
  description: ReactNode;
  confirmLabel?: string;
  destructive?: boolean;
  onConfirm: () => unknown;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  const [innerOpen, setInnerOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const open = controlledOpen ?? innerOpen;
  const setOpen = (v: boolean) => {
    if (!v) setError(null);
    setInnerOpen(v);
    onOpenChange?.(v);
  };
  const run = async () => {
    setBusy(true);
    setError(null);
    try {
      await onConfirm();
      setOpen(false);
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };
  return (
    <AlertDialog.Root open={open} onOpenChange={setOpen}>
      {trigger && <AlertDialog.Trigger asChild>{trigger}</AlertDialog.Trigger>}
      <AlertDialog.Portal>
        <AlertDialog.Overlay className={overlay} />
        <AlertDialog.Content className={clsx(content, 'max-w-md')}>
          <div className="px-4 pt-4">
            <AlertDialog.Title className="text-[15px] font-semibold">{title}</AlertDialog.Title>
            <AlertDialog.Description asChild>
              <div className="mt-1.5 text-[13px] text-muted">{description}</div>
            </AlertDialog.Description>
            <ErrorAlert error={error} className="mt-3" />
          </div>
          <div className="mt-4 flex justify-end gap-2 border-t border-line px-4 py-3">
            <AlertDialog.Cancel asChild>
              <Button>Cancel</Button>
            </AlertDialog.Cancel>
            <Button
              variant={destructive ? 'danger-solid' : 'primary'}
              loading={busy}
              onClick={(e) => {
                e.preventDefault();
                void run();
              }}
            >
              {confirmLabel}
            </Button>
          </div>
        </AlertDialog.Content>
      </AlertDialog.Portal>
    </AlertDialog.Root>
  );
}
