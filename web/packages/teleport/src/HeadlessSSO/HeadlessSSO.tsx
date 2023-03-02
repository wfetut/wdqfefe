/*
 *
 * Copyright 2023 Gravitational, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 * /
 *
 */

import React, {useState} from 'react';
import AuthnDialog from "teleport/components/AuthnDialog";
import auth from "teleport/services/auth";
import Dialog, {DialogHeader, DialogTitle} from "design/Dialog";
import {useParams} from "teleport/components/Router";


export function HeadlessSSO() {
    const {requestId} = useParams<{ requestId: string }>();

    const [state, setState] = useState({
        status: "",
        errorText: '',
        publicKey: null as PublicKeyCredentialRequestOptions,
    });

    const abcSuccess = () => {
        setState({...state, status: "success"})
    }

    return <div>
        <p>AAAAA</p>
        {state.status != "success" && (
            <AuthnDialog
                // onContinue={webauthn.authenticate}
                onContinue={() => {
                    // submitWithWebauthn()
                    setState({...state, status: "in-progress"})

                    auth.headlessSSOAccept(requestId)
                        .then(abcSuccess)
                        .catch((e) => {
                            setState({...state, status: "error", errorText: e})
                        });
                }}
                // onCancel={closeDocument}
                onCancel={() => {
                    window.close();
                }}
                errorText={state.errorText}
            />)}
        {state.status == "success" && (
            <SuccessDialog/>
        )}
    </div>
}

function SuccessDialog() {
    return (
        <Dialog dialogCss={() => ({width: '400px'})} open={true}>
            <DialogHeader style={{flexDirection: 'column'}}>
                <DialogTitle textAlign="center">
                    Success
                </DialogTitle>
            </DialogHeader>
        </Dialog>
    );
}