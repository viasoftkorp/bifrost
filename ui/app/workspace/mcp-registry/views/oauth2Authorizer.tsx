import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { getErrorMessage } from "@/lib/store/apis/baseApi";
import { useCompleteOAuthFlowMutation, useLazyGetOAuthConfigStatusQuery } from "@/lib/store/apis/mcpApi";
import { AlertTriangle, CheckCircle2, ExternalLink, KeyRound, Loader2, RefreshCw, ShieldCheck, XCircle } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

interface OAuth2AuthorizerProps {
	open: boolean;
	onClose: () => void;
	onSuccess: () => void;
	onError: (error: string) => void;
	authorizeUrl: string;
	oauthConfigId: string;
	mcpClientId: string;
	isPerUserOauth?: boolean;
}

export const OAuth2Authorizer: React.FC<OAuth2AuthorizerProps> = ({ open, onClose, onSuccess, onError, authorizeUrl, oauthConfigId, isPerUserOauth }) => {
	const [status, setStatus] = useState<"confirm" | "pending" | "blocked" | "polling" | "success" | "failed">(isPerUserOauth ? "confirm" : "pending");
	const [errorMessage, setErrorMessage] = useState<string | null>(null);
	const popupRef = useRef<Window | null>(null);
	const pollIntervalRef = useRef<NodeJS.Timeout | null>(null);
	const isCompletingRef = useRef(false);
	// Set to true when the user cancels so in-flight async callbacks do not
	// invoke onSuccess / onError / onClose after the dialog is dismissed.
	const cancelledRef = useRef(false);

	// RTK Query hooks
	const [getOAuthStatus] = useLazyGetOAuthConfigStatusQuery();
	const [completeOAuth] = useCompleteOAuthFlowMutation();
	const authorizationHost = useMemo(() => {
		try {
			return new URL(authorizeUrl).host;
		} catch {
			return "the OAuth provider";
		}
	}, [authorizeUrl]);

	// Stop polling
	const stopPolling = useCallback(() => {
		if (pollIntervalRef.current) {
			clearInterval(pollIntervalRef.current);
			pollIntervalRef.current = null;
		}
	}, []);

	// Handle successful OAuth completion
	const handleOAuthComplete = useCallback(async () => {
		if (cancelledRef.current) return;
		// Guard against concurrent calls (race between postMessage and polling)
		if (isCompletingRef.current) return;
		isCompletingRef.current = true;

		// Close popup if still open
		if (popupRef.current && !popupRef.current.closed) {
			popupRef.current.close();
		}

		// Call complete-oauth endpoint using RTK Query mutation
		// Use oauthConfigId instead of mcpClientId for multi-instance support
		try {
			await completeOAuth(oauthConfigId).unwrap();
			if (cancelledRef.current) return;
			setStatus("success");
			onSuccess();
		} catch (error) {
			if (cancelledRef.current) return;
			const errMsg = getErrorMessage(error);
			setStatus("failed");
			setErrorMessage(errMsg);
			onError(errMsg);
		}
	}, [oauthConfigId, completeOAuth, onSuccess, onError]);

	// Handle OAuth failure
	const handleOAuthFailed = useCallback(
		(reason: string) => {
			stopPolling();
			if (popupRef.current && !popupRef.current.closed) {
				popupRef.current.close();
			}
			if (cancelledRef.current) return;
			setStatus("failed");
			setErrorMessage(reason);
			onError(reason);
		},
		[stopPolling, onError],
	);

	// Check OAuth status (called by postMessage or polling)
	const checkOAuthStatus = useCallback(async () => {
		if (cancelledRef.current) return;
		try {
			const result = await getOAuthStatus(oauthConfigId).unwrap();
			if (cancelledRef.current) return;

			if (result.status === "authorized") {
				stopPolling();
				await handleOAuthComplete();
			} else if (result.status === "failed" || result.status === "expired") {
				handleOAuthFailed(`Authorization ${result.status}`);
			}
		} catch (error) {
			console.error("Error checking OAuth status:", error);
		}
	}, [oauthConfigId, getOAuthStatus, stopPolling, handleOAuthComplete, handleOAuthFailed]);

	// Poll OAuth status
	const startPolling = useCallback(() => {
		// Clear any existing interval
		if (pollIntervalRef.current) {
			clearInterval(pollIntervalRef.current);
		}

		pollIntervalRef.current = setInterval(async () => {
			// Check if popup is still open
			if (popupRef.current && popupRef.current.closed) {
				// Popup closed - check status before assuming cancellation
				// (OAuth callback page closes the popup after success)
				try {
					const result = await getOAuthStatus(oauthConfigId).unwrap();
					if (result.status === "authorized") {
						stopPolling();
						await handleOAuthComplete();
					} else if (result.status === "failed" || result.status === "expired") {
						stopPolling();
						handleOAuthFailed("Authorization failed");
					}
					// pending or other non-terminal: let polling continue
				} catch {
					// transient fetch error: let polling continue
				}
				return;
			}

			await checkOAuthStatus();
		}, 2000); // Poll every 2 seconds
	}, [checkOAuthStatus, getOAuthStatus, handleOAuthComplete, handleOAuthFailed, oauthConfigId, stopPolling]);

	// Open popup and start polling
	const openPopup = useCallback(() => {
		// Reset completion and cancelled guards for each fresh OAuth attempt
		isCompletingRef.current = false;
		cancelledRef.current = false;

		// Close any existing popup
		if (popupRef.current && !popupRef.current.closed) {
			popupRef.current.close();
		}

		// Open OAuth popup
		const width = 600;
		const height = 700;
		const left = window.screen.width / 2 - width / 2;
		const top = window.screen.height / 2 - height / 2;

		const popup = window.open(authorizeUrl, "oauth_popup", `width=${width},height=${height},left=${left},top=${top},resizable=yes,scrollbars=yes`);

		if (!popup || popup.closed) {
			popupRef.current = null;
			setStatus("blocked");
			return;
		}

		popupRef.current = popup;
		setStatus("polling");

		// Start polling OAuth status
		startPolling();
	}, [authorizeUrl, startPolling]);

	// Listen for postMessage from OAuth callback popup
	useEffect(() => {
		const handleMessage = (event: MessageEvent) => {
			// Only accept messages from the popup we opened and our own callback origin.
			if (event.source !== popupRef.current || event.origin !== window.location.origin) {
				return;
			}

			if (event.data?.type === "oauth_success") {
				// Trigger immediate status check; stopPolling is called inside
				// checkOAuthStatus only after a confirmed terminal state, so
				// transient fetch errors still allow polling to continue.
				checkOAuthStatus();
			}
		};

		window.addEventListener("message", handleMessage);
		return () => {
			window.removeEventListener("message", handleMessage);
		};
	}, [checkOAuthStatus]);

	// Handle user confirming per-user OAuth test
	const handleConfirmPerUserOAuth = () => {
		openPopup();
	};

	// Cleanup on unmount
	useEffect(() => {
		return () => {
			stopPolling();
			if (popupRef.current && !popupRef.current.closed) {
				popupRef.current.close();
			}
		};
	}, [stopPolling]);

	const handleRetry = () => {
		setErrorMessage(null);
		isCompletingRef.current = false;
		if (isPerUserOauth) {
			setStatus("confirm");
		} else {
			openPopup();
		}
	};

	const handleCancel = () => {
		cancelledRef.current = true;
		stopPolling();
		isCompletingRef.current = false;
		if (popupRef.current && !popupRef.current.closed) {
			popupRef.current.close();
		}
		onClose();
	};

	const title = status === "confirm" ? "Verify OAuth setup" : "Authorize connection";
	const description = {
		confirm: "Run a one-time OAuth test before enabling this server.",
		pending: "Open a secure authorization window to continue.",
		blocked: "The authorization window was blocked.",
		polling: "Waiting for the OAuth provider to confirm access.",
		success: "OAuth authorization completed.",
		failed: "OAuth authorization failed.",
	}[status];

	return (
		<Dialog
			open={open}
			onOpenChange={(nextOpen) => {
				if (!nextOpen) {
					handleCancel();
				}
			}}
		>
			<DialogContent
				className="gap-0 overflow-hidden p-0 sm:max-w-lg"
				onPointerDownOutside={(e) => {
					e.preventDefault();
					handleCancel();
				}}
				onEscapeKeyDown={(e) => {
					e.preventDefault();
					handleCancel();
				}}
			>
				<DialogHeader className="border-b px-6 py-5 text-left">
					<div className="flex items-start gap-3">
						<div className="bg-primary/10 text-primary flex size-10 shrink-0 items-center justify-center rounded-sm border">
							{status === "polling" ? (
								<Loader2 className="size-5 animate-spin" />
							) : status === "success" ? (
								<CheckCircle2 className="size-5" />
							) : status === "failed" ? (
								<XCircle className="size-5" />
							) : status === "blocked" ? (
								<AlertTriangle className="size-5" />
							) : (
								<ShieldCheck className="size-5" />
							)}
						</div>
						<div className="min-w-0 space-y-1">
							<DialogTitle>{title}</DialogTitle>
							<DialogDescription>{description}</DialogDescription>
						</div>
					</div>
				</DialogHeader>

				<div className="space-y-4 px-6 py-5">
					{status === "confirm" && (
						<>
							<div className="rounded-sm border bg-muted/20 p-4">
								<div className="flex gap-3">
									<KeyRound className="text-muted-foreground mt-0.5 size-4 shrink-0" />
									<div className="space-y-2 text-sm">
										<p>We will open {authorizationHost} to verify the OAuth configuration and discover available tools.</p>
										<p className="text-muted-foreground">
											This login is only used for setup verification. Each user will authenticate individually when they use this MCP
											server.
										</p>
									</div>
								</div>
							</div>
							<div className="flex w-full justify-end space-x-2">
								<Button onClick={handleCancel} variant="outline" data-testid="per-user-oauth-cancel">
									Cancel
								</Button>
								<Button onClick={handleConfirmPerUserOAuth} data-testid="per-user-oauth-confirm">
									<ExternalLink className="size-4" />
									Continue
								</Button>
							</div>
						</>
					)}

					{(status === "pending" || status === "blocked") && (
						<>
							<div className="rounded-sm border bg-muted/20 p-4">
								<div className="flex gap-3">
									{status === "blocked" ? (
										<AlertTriangle className="text-amber-600 mt-0.5 size-4 shrink-0" />
									) : (
										<ExternalLink className="text-muted-foreground mt-0.5 size-4 shrink-0" />
									)}
									<div className="space-y-1 text-sm">
										<p className="font-medium">
											{status === "blocked" ? "Allow popups, then try again." : "Sign in with the OAuth provider."}
										</p>
										<p className="text-muted-foreground">
											{status === "blocked"
												? "Your browser prevented Bifrost from opening the authorization window automatically."
												: `Bifrost will open ${authorizationHost} in a separate window and listen for the callback.`}
										</p>
									</div>
								</div>
							</div>
							<div className="flex w-full justify-end space-x-2">
								<Button onClick={handleCancel} variant="outline" data-testid="oauth-pending-cancel-btn">
									Cancel
								</Button>
								<Button onClick={openPopup} data-testid="oauth-open-window-btn">
									<ExternalLink className="size-4" />
									Open authorization
								</Button>
							</div>
						</>
					)}

					{status === "polling" && (
						<>
							<div className="rounded-sm border bg-muted/20 p-4">
								<div className="flex gap-3">
									<Loader2 className="text-primary mt-0.5 size-4 shrink-0 animate-spin" />
									<div className="space-y-1 text-sm">
										<p className="font-medium">Complete authorization in the popup window.</p>
										<p className="text-muted-foreground">
											This dialog will update automatically after the provider redirects back to Bifrost.
										</p>
									</div>
								</div>
							</div>
							<div className="flex justify-end">
								<Button onClick={handleCancel} variant="outline">
									Cancel
								</Button>
							</div>
						</>
					)}

					{status === "success" && (
						<div className="rounded-sm border border-green-200/50 bg-green-50/60 p-4 text-green-900 dark:border-green-800/40 dark:bg-green-950/30 dark:text-green-100">
							<div className="flex gap-3">
								<CheckCircle2 className="mt-0.5 size-4 shrink-0" />
								<div className="space-y-1 text-sm">
									<p className="font-medium">Connection authorized.</p>
									<p className="text-green-700 dark:text-green-200">Bifrost is finishing setup and syncing the MCP server tools.</p>
								</div>
							</div>
						</div>
					)}

					{status === "failed" && (
						<>
							<div className="rounded-sm border border-red-200/50 bg-red-50/60 p-4 text-red-900 dark:border-red-800/40 dark:bg-red-950/30 dark:text-red-100">
								<div className="flex gap-3">
									<XCircle className="mt-0.5 size-4 shrink-0" />
									<div className="space-y-1 text-sm">
										<p className="font-medium">Authorization did not complete.</p>
										<p className="text-red-700 dark:text-red-200">
											{errorMessage || "Try again, or check the OAuth provider configuration."}
										</p>
									</div>
								</div>
							</div>
							<div className="flex justify-end gap-2">
								<Button onClick={handleCancel} variant="outline">
									Close
								</Button>
								<Button onClick={handleRetry}>
									<RefreshCw className="size-4" />
									Retry
								</Button>
							</div>
						</>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
};
