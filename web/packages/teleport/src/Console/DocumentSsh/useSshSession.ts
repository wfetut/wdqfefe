/*
Copyright 2020 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

import React from 'react';

import { context, trace } from '@opentelemetry/api';

import cfg from 'teleport/config';
import { TermEvent } from 'teleport/lib/term/enums';
import Tty from 'teleport/lib/term/tty';
import ConsoleContext from 'teleport/Console/consoleContext';
import { useConsoleContext } from 'teleport/Console/consoleContextProvider';
import { DocumentSsh } from 'teleport/Console/stores';

import type {
  ParticipantMode,
  Session,
  SessionMetadata,
} from 'teleport/services/session';

const tracer = trace.getTracer('TTY');

let totalBytesReceived = 0;
let fileData = new ArrayBuffer(2372824);
let totalMessages = 0;
/* let blob = new Blob([], { type: 'application/octet-stream' }); */
/**/
/* const stream = new WritableStream({ */
/*   write(chunk) { */
/*     console.log('blob', blob); */
/*     blob = new Blob([blob, chunk], { type: 'application/octet-stream' }); */
/*   }, */
/* }); */

export default function useSshSession(doc: DocumentSsh) {
  const { clusterId, sid, serverId, login, mode } = doc;
  const ctx = useConsoleContext();
  const ttyRef = React.useRef<Tty>(null);
  const tty = ttyRef.current as ReturnType<typeof ctx.createTty>;
  const [session, setSession] = React.useState<Session>(null);
  const [status, setStatus] = React.useState<Status>('loading');

  function closeDocument() {
    ctx.closeTab(doc);
  }

  React.useEffect(() => {
    // initializes tty instances
    function initTty(session, mode?: ParticipantMode) {
      tracer.startActiveSpan(
        'initTTY',
        undefined, // SpanOptions
        context.active(),
        span => {
          const tty = ctx.createTty(session, mode);

          // subscribe to tty events to handle connect/disconnects events
          tty.on(TermEvent.CLOSE, () => ctx.closeTab(doc));

          tty.on(TermEvent.FILE_TRANSFER_RAW, payload => {
            totalMessages++;
            /* console.log(payload); */
            /* console.log(totalBytesReceived % fileData.byteLength); */
            const dataView = new DataView(fileData, totalBytesReceived);
            for (let i = 0; i < payload.length; i++) {
              dataView.setUint8(i, payload[i]);
              /* dataView.setUint8( */
              /*   totalBytesReceived % fileData.byteLength, */
              /*   payload[i] */
              /* ); */
            }
            console.log(
              `message number: ${totalMessages}: current: ${totalBytesReceived}. adding ${
                payload.length
              } for a total of ${totalBytesReceived + payload.length}`
            );
            totalBytesReceived = totalBytesReceived + payload.length;

            if (totalBytesReceived === fileData.byteLength) {
              console.log('done');
              saveOnDisk(
                'neon.png',
                new Blob([fileData], { type: 'application/octet-stream' })
              );
            }
          });

          tty.on(TermEvent.CONN_CLOSE, () =>
            ctx.updateSshDocument(doc.id, { status: 'disconnected' })
          );

          tty.on(TermEvent.SESSION, payload => {
            const data = JSON.parse(payload);
            data.session.kind = 'ssh';
            data.session.resourceName = data.session.server_hostname;
            handleTtyConnect(ctx, data.session, doc.id);
          });

          // assign tty reference so it can be passed down to xterm
          ttyRef.current = tty;
          setSession(session);
          setStatus('initialized');
          span.end();
        }
      );
    }

    // cleanup by unsubscribing from tty
    function cleanup() {
      ttyRef.current && ttyRef.current.removeAllListeners();
    }

    initTty(
      {
        login,
        serverId,
        clusterId,
        sid,
      },
      mode
    );

    return cleanup;

    // Only run this once on the initial render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function download() {
    console.log(totalBytesReceived);
    /* saveOnDisk('masters.zip', blob); */
  }

  return {
    tty,
    status,
    session,
    closeDocument,
    download,
  };
}

function handleTtyConnect(
  ctx: ConsoleContext,
  session: SessionMetadata,
  docId: number
) {
  const {
    resourceName,
    login,
    id: sid,
    cluster_name: clusterId,
    server_id: serverId,
    created,
  } = session;

  const url = cfg.getSshSessionRoute({ sid, clusterId });
  const createdDate = new Date(created);
  ctx.updateSshDocument(docId, {
    title: `${login}@${resourceName}`,
    status: 'connected',
    url,
    serverId,
    created: createdDate,
    login,
    sid,
    clusterId,
  });

  ctx.gotoTab({ url });
}

function saveOnDisk(fileName: string, blob: Blob): void {
  const a = document.createElement('a');
  a.href = window.URL.createObjectURL(blob);
  a.download = fileName;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
}

type Status = 'initialized' | 'loading';
