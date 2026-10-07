import { useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { RefreshCw } from 'lucide-react';
import { useState } from 'react';
import { versionQuery } from '@/api/queries';
import { CliCommand } from '@/components/cliHint';
import { Button } from '@/components/ui';
import { cli } from '@/lib/cli';
import { AuthLayout } from './AuthLayout';

/**
 * Shown while the server has no users. The organisation and the owner
 * account are created from the terminal; this page says how and checks
 * again when asked.
 */
export function SetupPage() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [checking, setChecking] = useState(false);
  const [still, setStill] = useState(false);

  const check = async () => {
    setChecking(true);
    try {
      qc.removeQueries({ queryKey: versionQuery.queryKey });
      const v = await qc.query(versionQuery);
      if (v.setupRequired) setStill(true);
      else await navigate({ to: '/login' });
    } finally {
      setChecking(false);
    }
  };

  return (
    <AuthLayout
      title="Set up Stampede"
      subtitle="This server has no organisation yet. Create it and the owner account from the terminal; the web UI is for analysis and reporting."
    >
      <ol className="flex list-decimal flex-col gap-3 pl-5 text-[13px]">
        <li>
          Install the <span className="font-mono">stampede</span> CLI on a machine that can reach{' '}
          <span className="font-mono break-all">{window.location.origin}</span>.
        </li>
        <li className="flex flex-col gap-1.5">
          Run the setup and answer its questions:
          <CliCommand command={cli.setup} className="self-start" />
        </li>
        <li>Come back here and sign in.</li>
      </ol>
      <div className="mt-5 flex flex-col gap-2">
        <Button variant="primary" loading={checking} onClick={() => void check()}>
          <RefreshCw className="size-3.5" aria-hidden /> I have run it, continue
        </Button>
        {still && (
          <p role="status" className="text-center text-xs text-muted">
            The server still needs setting up. Run <code className="font-mono">stampede setup</code>{' '}
            first.
          </p>
        )}
      </div>
    </AuthLayout>
  );
}
