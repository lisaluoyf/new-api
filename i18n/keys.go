package i18n

// Message keys for i18n translations
// Use these constants instead of hardcoded strings

// Common error messages
const (
	MsgInvalidParams      = "common.invalid_params"
	MsgDatabaseError      = "common.database_error"
	MsgRetryLater         = "common.retry_later"
	MsgGenerateFailed     = "common.generate_failed"
	MsgNotFound           = "common.not_found"
	MsgUnauthorized       = "common.unauthorized"
	MsgForbidden          = "common.forbidden"
	MsgInvalidId          = "common.invalid_id"
	MsgIdEmpty            = "common.id_empty"
	MsgFeatureDisabled    = "common.feature_disabled"
	MsgOperationSuccess   = "common.operation_success"
	MsgOperationFailed    = "common.operation_failed"
	MsgUpdateSuccess      = "common.update_success"
	MsgUpdateFailed       = "common.update_failed"
	MsgCreateSuccess      = "common.create_success"
	MsgCreateFailed       = "common.create_failed"
	MsgDeleteSuccess      = "common.delete_success"
	MsgDeleteFailed       = "common.delete_failed"
	MsgAlreadyExists      = "common.already_exists"
	MsgNameCannotBeEmpty  = "common.name_cannot_be_empty"
	MsgBatchTooMany       = "common.batch_too_many"
	MsgEndpointDeprecated = "common.endpoint_deprecated"
)

// Auth middleware messages
const (
	MsgAuthNotLoggedIn              = "auth.not_logged_in"
	MsgAuthAccessTokenInvalid       = "auth.access_token_invalid"
	MsgAuthUserInfoInvalid          = "auth.user_info_invalid"
	MsgAuthUserIdNotProvided        = "auth.user_id_not_provided"
	MsgAuthUserIdFormatError        = "auth.user_id_format_error"
	MsgAuthUserIdMismatch           = "auth.user_id_mismatch"
	MsgAuthUserBanned               = "auth.user_banned"
	MsgAuthInsufficientPrivilege    = "auth.insufficient_privilege"
	MsgAuthClientIPInvalid          = "auth.client_ip_invalid"
	MsgAuthClientIPForbidden        = "auth.client_ip_forbidden"
	MsgAuthGroupForbidden           = "auth.group_forbidden"
	MsgAuthGroupDeprecated          = "auth.group_deprecated"
	MsgAuthSpecificChannelForbidden = "auth.specific_channel_forbidden"
)

// Token related messages
const (
	MsgTokenNameTooLong          = "token.name_too_long"
	MsgTokenQuotaNegative        = "token.quota_negative"
	MsgTokenQuotaExceedMax       = "token.quota_exceed_max"
	MsgTokenGenerateFailed       = "token.generate_failed"
	MsgTokenGetInfoFailed        = "token.get_info_failed"
	MsgTokenExpiredCannotEnable  = "token.expired_cannot_enable"
	MsgTokenExhaustedCannotEable = "token.exhausted_cannot_enable"
	MsgTokenInvalid              = "token.invalid"
	MsgTokenNotProvided          = "token.not_provided"
	MsgTokenExpired              = "token.expired"
	MsgTokenExhausted            = "token.exhausted"
	MsgTokenStatusUnavailable    = "token.status_unavailable"
	MsgTokenDbError              = "token.db_error"
	MsgTokenCountLimit           = "token.count_limit"
)

// Redemption related messages
const (
	MsgRedemptionNameLength        = "redemption.name_length"
	MsgRedemptionCountPositive     = "redemption.count_positive"
	MsgRedemptionCountMax          = "redemption.count_max"
	MsgRedemptionCreateFailed      = "redemption.create_failed"
	MsgRedemptionInvalid           = "redemption.invalid"
	MsgRedemptionUsed              = "redemption.used"
	MsgRedemptionExpired           = "redemption.expired"
	MsgRedemptionFailed            = "redemption.failed"
	MsgRedemptionNotProvided       = "redemption.not_provided"
	MsgRedemptionExpireTimeInvalid = "redemption.expire_time_invalid"
)

// User related messages
const (
	MsgUserPasswordLoginDisabled     = "user.password_login_disabled"
	MsgUserRegisterDisabled          = "user.register_disabled"
	MsgUserPasswordRegisterDisabled  = "user.password_register_disabled"
	MsgUserUsernameOrPasswordEmpty   = "user.username_or_password_empty"
	MsgUserUsernameOrPasswordError   = "user.username_or_password_error"
	MsgUserEmailOrPasswordEmpty      = "user.email_or_password_empty"
	MsgUserExists                    = "user.exists"
	MsgUserNotExists                 = "user.not_exists"
	MsgUserDisabled                  = "user.disabled"
	MsgUserSessionSaveFailed         = "user.session_save_failed"
	MsgUserRequire2FA                = "user.require_2fa"
	MsgUserEmailVerificationRequired = "user.email_verification_required"
	MsgUserVerificationCodeError     = "user.verification_code_error"
	MsgUserInputInvalid              = "user.input_invalid"
	MsgUserNoPermissionSameLevel     = "user.no_permission_same_level"
	MsgUserNoPermissionHigherLevel   = "user.no_permission_higher_level"
	MsgUserCannotCreateHigherLevel   = "user.cannot_create_higher_level"
	MsgUserCannotDeleteRootUser      = "user.cannot_delete_root_user"
	MsgUserCannotDisableRootUser     = "user.cannot_disable_root_user"
	MsgUserCannotDemoteRootUser      = "user.cannot_demote_root_user"
	MsgUserAlreadyAdmin              = "user.already_admin"
	MsgUserAlreadyCommon             = "user.already_common"
	MsgUserAdminCannotPromote        = "user.admin_cannot_promote"
	MsgUserOriginalPasswordError     = "user.original_password_error"
	MsgUserInviteQuotaInsufficient   = "user.invite_quota_insufficient"
	MsgUserTransferQuotaMinimum      = "user.transfer_quota_minimum"
	MsgUserTransferSuccess           = "user.transfer_success"
	MsgUserTransferFailed            = "user.transfer_failed"
	MsgUserTopUpProcessing           = "user.topup_processing"
	MsgUserRegisterFailed            = "user.register_failed"
	MsgUserDefaultTokenFailed        = "user.default_token_failed"
	MsgUserAffCodeEmpty              = "user.aff_code_empty"
	MsgUserEmailEmpty                = "user.email_empty"
	MsgUserGitHubIdEmpty             = "user.github_id_empty"
	MsgUserDiscordIdEmpty            = "user.discord_id_empty"
	MsgUserOidcIdEmpty               = "user.oidc_id_empty"
	MsgUserWeChatIdEmpty             = "user.wechat_id_empty"
	MsgUserTelegramIdEmpty           = "user.telegram_id_empty"
	MsgUserTelegramNotBound          = "user.telegram_not_bound"
	MsgUserLinuxDOIdEmpty            = "user.linux_do_id_empty"
	MsgUserQuotaChangeZero           = "user.quota_change_zero"
)

// Quota related messages
const (
	MsgQuotaNegative        = "quota.negative"
	MsgQuotaExceedMax       = "quota.exceed_max"
	MsgQuotaInsufficient    = "quota.insufficient"
	MsgQuotaWarningInvalid  = "quota.warning_invalid"
	MsgQuotaThresholdGtZero = "quota.threshold_gt_zero"
)

// Subscription related messages
const (
	MsgSubscriptionNotEnabled       = "subscription.not_enabled"
	MsgSubscriptionTitleEmpty       = "subscription.title_empty"
	MsgSubscriptionPriceNegative    = "subscription.price_negative"
	MsgSubscriptionPriceMax         = "subscription.price_max"
	MsgSubscriptionPurchaseLimitNeg = "subscription.purchase_limit_negative"
	MsgSubscriptionQuotaNegative    = "subscription.quota_negative"
	MsgSubscriptionGroupNotExists   = "subscription.group_not_exists"
	MsgSubscriptionResetCycleGtZero = "subscription.reset_cycle_gt_zero"
	MsgSubscriptionPurchaseMax      = "subscription.purchase_max"
	MsgSubscriptionInvalidId        = "subscription.invalid_id"
	MsgSubscriptionInvalidUserId    = "subscription.invalid_user_id"
	MsgSubscriptionTierQuotaNeg     = "subscription.tier_quota_negative"
	MsgSubscriptionInvalidType      = "subscription.invalid_type"
	MsgSubscriptionPaidOnlyDelete   = "subscription.paid_only_delete"
	MsgSubscriptionHasRecords       = "subscription.has_records"
	MsgSubscriptionSoldOutCoding    = "subscription.sold_out_coding_only"
	MsgSubscriptionInvalidSubId     = "subscription.invalid_subscription_id"
	MsgSubscriptionEmailAllowlist   = "subscription.invalid_email_allowlist"
	MsgSubscriptionOrderType        = "subscription.invalid_order_type"
)

// Payment related messages
const (
	MsgPaymentNotConfigured    = "payment.not_configured"
	MsgPaymentMethodNotExists  = "payment.method_not_exists"
	MsgPaymentCallbackError    = "payment.callback_error"
	MsgPaymentCreateFailed     = "payment.create_failed"
	MsgPaymentStartFailed      = "payment.start_failed"
	MsgPaymentAmountTooLow     = "payment.amount_too_low"
	MsgPaymentStripeNotConfig  = "payment.stripe_not_configured"
	MsgPaymentWebhookNotConfig = "payment.webhook_not_configured"
	MsgPaymentPriceIdNotConfig = "payment.price_id_not_configured"
	MsgPaymentCreemNotConfig   = "payment.creem_not_configured"
)

// Topup related messages
const (
	MsgTopupNotProvided         = "topup.not_provided"
	MsgTopupOrderNotExists      = "topup.order_not_exists"
	MsgTopupOrderStatus         = "topup.order_status"
	MsgTopupFailed              = "topup.failed"
	MsgTopupInvalidQuota        = "topup.invalid_quota"
	MsgTopupAmountTooLow        = "topup.amount_too_low"
	MsgTopupAmountMin           = "topup.amount_min"
	MsgTopupAmountMax           = "topup.amount_max"
	MsgPaymentRedirectUntrusted = "payment.redirect_untrusted"
)

// Channel related messages
const (
	MsgChannelNotExists          = "channel.not_exists"
	MsgChannelIdFormatError      = "channel.id_format_error"
	MsgChannelNoAvailableKey     = "channel.no_available_key"
	MsgChannelGetListFailed      = "channel.get_list_failed"
	MsgChannelGetTagsFailed      = "channel.get_tags_failed"
	MsgChannelGetKeyFailed       = "channel.get_key_failed"
	MsgChannelGetOllamaFailed    = "channel.get_ollama_failed"
	MsgChannelQueryFailed        = "channel.query_failed"
	MsgChannelNoValidUpstream    = "channel.no_valid_upstream"
	MsgChannelUpstreamSaturated  = "channel.upstream_saturated"
	MsgChannelGetAvailableFailed = "channel.get_available_failed"
	MsgChannelCredentialRefresh  = "channel.credential_refresh_failed"
	MsgChannelUnsupportedAddMode = "channel.unsupported_add_mode"
	MsgChannelTagEmpty           = "channel.tag_empty"
	MsgChannelParamJSONInvalid   = "channel.param_override_json_invalid"
	MsgChannelHeaderJSONInvalid  = "channel.header_override_json_invalid"
	MsgChannelAppendKeyInvalid   = "channel.append_key_invalid"
	MsgChannelCloneFailed        = "channel.clone_failed"
	MsgChannelMultiKeyRequired   = "channel.multi_key_required"
	MsgChannelKeyIndexDisable    = "channel.key_index_disable_required"
	MsgChannelKeyIndexEnable     = "channel.key_index_enable_required"
	MsgChannelKeyIndexDelete     = "channel.key_index_delete_required"
	MsgChannelKeyIndexRange      = "channel.key_index_out_of_range"
	MsgChannelKeyDisabled        = "channel.key_disabled"
	MsgChannelKeyEnabled         = "channel.key_enabled"
	MsgChannelKeyDeleted         = "channel.key_deleted"
	MsgChannelNoKeyToDisable     = "channel.no_key_to_disable"
	MsgChannelLastKeyDelete      = "channel.last_key_delete_forbidden"
	MsgChannelNoAutoKeyDelete    = "channel.no_auto_disabled_key"
	MsgChannelUnsupportedAction  = "channel.unsupported_action"
	MsgChannelMultiKeyBalance    = "channel.multi_key_balance_unsupported"
	MsgChannelFetchModelsFailed  = "channel.fetch_models_failed"
	MsgChannelKeysEnabled        = "channel.keys_enabled"
	MsgChannelKeysDisabled       = "channel.keys_disabled"
	MsgChannelKeysDeleted        = "channel.keys_deleted"
	MsgChannelOllamaOnly         = "channel.ollama_only"
	MsgChannelFetchVersionFailed = "channel.fetch_version_failed"
)

// Model related messages
const (
	MsgModelNameEmpty           = "model.name_empty"
	MsgModelNameExists          = "model.name_exists"
	MsgModelIdMissing           = "model.id_missing"
	MsgModelGetListFailed       = "model.get_list_failed"
	MsgModelGetFailed           = "model.get_failed"
	MsgModelResetSuccess        = "model.reset_success"
	MsgModelUpstreamFetchFailed = "model.upstream_fetch_failed"
)

// Vendor related messages
const (
	MsgVendorNameEmpty  = "vendor.name_empty"
	MsgVendorNameExists = "vendor.name_exists"
	MsgVendorIdMissing  = "vendor.id_missing"
)

// Group related messages
const (
	MsgGroupNameTypeEmpty = "group.name_type_empty"
	MsgGroupNameExists    = "group.name_exists"
	MsgGroupIdMissing     = "group.id_missing"
)

// Checkin related messages
const (
	MsgCheckinDisabled     = "checkin.disabled"
	MsgCheckinAlreadyToday = "checkin.already_today"
	MsgCheckinFailed       = "checkin.failed"
	MsgCheckinQuotaFailed  = "checkin.quota_failed"
	MsgCheckinSuccess      = "checkin.success"
)

// Passkey related messages
const (
	MsgPasskeyCreateFailed         = "passkey.create_failed"
	MsgPasskeyLoginAbnormal        = "passkey.login_abnormal"
	MsgPasskeyUpdateFailed         = "passkey.update_failed"
	MsgPasskeyInvalidUserId        = "passkey.invalid_user_id"
	MsgPasskeyVerifyFailed         = "passkey.verify_failed"
	MsgPasskeyVerificationRequired = "passkey.verification_required"
	MsgPasskeyMatchingVerification = "passkey.matching_verification_required"
	MsgPasskeyNotBound             = "passkey.not_bound"
	MsgPasskeyRegisterSuccess      = "passkey.register_success"
	MsgPasskeyUnbindSuccess        = "passkey.unbind_success"
	MsgPasskeyResetSuccess         = "passkey.reset_success"
	MsgPasskeyVerifySuccess        = "passkey.verify_success"
)

// 2FA related messages
const (
	MsgTwoFANotEnabled       = "twofa.not_enabled"
	MsgTwoFAUserIdEmpty      = "twofa.user_id_empty"
	MsgTwoFAAlreadyExists    = "twofa.already_exists"
	MsgTwoFARecordIdEmpty    = "twofa.record_id_empty"
	MsgTwoFACodeInvalid      = "twofa.code_invalid"
	MsgTwoFAAlreadyEnabled   = "twofa.already_enabled"
	MsgTwoFASecretFailed     = "twofa.secret_generate_failed"
	MsgTwoFABackupFailed     = "twofa.backup_generate_failed"
	MsgTwoFABackupSaveFailed = "twofa.backup_save_failed"
	MsgTwoFASetupSuccess     = "twofa.setup_success"
	MsgTwoFASetupRequired    = "twofa.setup_required"
	MsgTwoFAEnableSuccess    = "twofa.enable_success"
	MsgTwoFADisableSuccess   = "twofa.disable_success"
	MsgTwoFABackupSuccess    = "twofa.backup_regenerate_success"
	MsgTwoFASessionExpired   = "twofa.session_expired"
	MsgTwoFASessionInvalid   = "twofa.session_invalid"
	MsgTwoFAAdminForbidden   = "twofa.admin_forbidden"
	MsgTwoFAAdminDisabled    = "twofa.admin_disable_success"
)

// Secure verification related messages
const (
	MsgSecureNotLoggedIn         = "secure.not_logged_in"
	MsgSecureVerificationReq     = "secure.verification_required"
	MsgSecureVerificationInvalid = "secure.verification_invalid"
	MsgSecureVerificationExpired = "secure.verification_expired"
	MsgSecureUserInfoFailed      = "secure.user_info_failed"
	MsgSecureNotEnabled          = "secure.not_enabled"
	MsgSecureTwoFANotEnabled     = "secure.twofa_not_enabled"
	MsgSecureCodeEmpty           = "secure.code_empty"
	MsgSecurePasskeyNotEnabled   = "secure.passkey_not_enabled"
	MsgSecurePasskeyStateInvalid = "secure.passkey_state_invalid"
	MsgSecurePasskeyFinishReq    = "secure.passkey_finish_required"
	MsgSecureMethodUnsupported   = "secure.method_unsupported"
	MsgSecureVerifyFailed        = "secure.verify_failed"
	MsgSecureSessionSaveFailed   = "secure.session_save_failed"
	MsgSecureVerifySuccess       = "secure.verify_success"
)

// Rate limit related messages
const (
	MsgRateLimitReached                  = "rate_limit.reached"
	MsgRateLimitTotalReached             = "rate_limit.total_reached"
	MsgEmailVerificationRateLimited      = "rate_limit.email_verification"
	MsgEmailVerificationRateLimitedLater = "rate_limit.email_verification_later"
)

// Turnstile related messages
const (
	MsgTurnstileTokenEmpty         = "turnstile.token_empty"
	MsgTurnstileUnavailable        = "turnstile.unavailable"
	MsgTurnstileVerificationFailed = "turnstile.verification_failed"
)

// Setting related messages
const (
	MsgSettingInvalidType             = "setting.invalid_type"
	MsgSettingWebhookEmpty            = "setting.webhook_empty"
	MsgSettingWebhookInvalid          = "setting.webhook_invalid"
	MsgSettingEmailInvalid            = "setting.email_invalid"
	MsgSettingBarkUrlEmpty            = "setting.bark_url_empty"
	MsgSettingBarkUrlInvalid          = "setting.bark_url_invalid"
	MsgSettingGotifyUrlEmpty          = "setting.gotify_url_empty"
	MsgSettingGotifyTokenEmpty        = "setting.gotify_token_empty"
	MsgSettingGotifyUrlInvalid        = "setting.gotify_url_invalid"
	MsgSettingUrlMustHttp             = "setting.url_must_http"
	MsgSettingSaved                   = "setting.saved"
	MsgSettingOAuthConfigRequired     = "setting.oauth_config_required"
	MsgSettingEmailDomainRequired     = "setting.email_domain_required"
	MsgSettingWeChatConfigRequired    = "setting.wechat_config_required"
	MsgSettingTurnstileConfigRequired = "setting.turnstile_config_required"
	MsgSettingTelegramBotRequired     = "setting.telegram_bot_required"
	MsgSettingTelegramSecretInvalid   = "setting.telegram_secret_invalid"
	MsgSettingThemeInvalid            = "setting.theme_invalid"
	MsgSettingRatioUpdateFailed       = "setting.ratio_update_failed"
)

// Email related messages
const (
	MsgEmailInvalid           = "email.invalid"
	MsgEmailDomainNotAllowed  = "email.domain_not_allowed"
	MsgEmailAliasNotAllowed   = "email.alias_not_allowed"
	MsgEmailAlreadyTaken      = "email.already_taken"
	MsgEmailResetLinkInvalid  = "email.reset_link_invalid"
	MsgEmailDomainUnavailable = "email.domain_unavailable"
)

// Usage related messages
const (
	MsgUsageTimeRangeTooLong = "usage.time_range_too_long"
)

// Initial setup related messages
const (
	MsgSetupAlreadyComplete      = "setup.already_complete"
	MsgSetupUsernameTooLong      = "setup.username_too_long"
	MsgSetupPasswordMismatch     = "setup.password_mismatch"
	MsgSetupPasswordTooShort     = "setup.password_too_short"
	MsgSetupSystemError          = "setup.system_error"
	MsgSetupCreateAdminFailed    = "setup.create_admin_failed"
	MsgSetupSaveModeFailed       = "setup.save_mode_failed"
	MsgSetupInitializationFailed = "setup.initialization_failed"
	MsgSetupSuccess              = "setup.success"
)

// Codex channel related messages
const (
	MsgCodexChannelTypeInvalid        = "codex.channel_type_invalid"
	MsgCodexMultiKeyUnsupported       = "codex.multi_key_unsupported"
	MsgCodexCredentialInvalid         = "codex.credential_invalid"
	MsgCodexAccessTokenRequired       = "codex.access_token_required"
	MsgCodexAccountIDRequired         = "codex.account_id_required"
	MsgCodexUsageFailed               = "codex.usage_failed"
	MsgCodexAuthorizationInputInvalid = "codex.authorization_input_invalid"
	MsgCodexOAuthSessionExpired       = "codex.oauth_session_expired"
	MsgCodexCodeExchangeFailed        = "codex.code_exchange_failed"
	MsgCodexAccountIDExtractFailed    = "codex.account_id_extract_failed"
)

// Deployment related messages (io.net)
const (
	MsgDeploymentNotEnabled     = "deployment.not_enabled"
	MsgDeploymentIdRequired     = "deployment.id_required"
	MsgDeploymentContainerIdReq = "deployment.container_id_required"
	MsgDeploymentNameEmpty      = "deployment.name_empty"
	MsgDeploymentNameTaken      = "deployment.name_taken"
	MsgDeploymentHardwareIdReq  = "deployment.hardware_id_required"
	MsgDeploymentHardwareInvId  = "deployment.hardware_invalid_id"
	MsgDeploymentApiKeyRequired = "deployment.api_key_required"
	MsgDeploymentInvalidPayload = "deployment.invalid_payload"
	MsgDeploymentNotFound       = "deployment.not_found"
)

// Performance related messages
const (
	MsgPerfDiskCacheCleared = "performance.disk_cache_cleared"
	MsgPerfStatsReset       = "performance.stats_reset"
	MsgPerfGcExecuted       = "performance.gc_executed"
)

// Ability related messages
const (
	MsgAbilityDbCorrupted   = "ability.db_corrupted"
	MsgAbilityRepairRunning = "ability.repair_running"
)

// OAuth related messages
const (
	MsgOAuthInvalidCode     = "oauth.invalid_code"
	MsgOAuthGetUserErr      = "oauth.get_user_error"
	MsgOAuthAccountUsed     = "oauth.account_used"
	MsgOAuthUnknownProvider = "oauth.unknown_provider"
	MsgOAuthStateInvalid    = "oauth.state_invalid"
	MsgOAuthNotEnabled      = "oauth.not_enabled"
	MsgOAuthUserDeleted     = "oauth.user_deleted"
	MsgOAuthUserBanned      = "oauth.user_banned"
	MsgOAuthBindSuccess     = "oauth.bind_success"
	MsgOAuthAlreadyBound    = "oauth.already_bound"
	MsgOAuthConnectFailed   = "oauth.connect_failed"
	MsgOAuthTokenFailed     = "oauth.token_failed"
	MsgOAuthUserInfoEmpty   = "oauth.user_info_empty"
	MsgOAuthTrustLevelLow   = "oauth.trust_level_low"
)

// Model layer error messages (for translation in controller)
const (
	MsgRedeemFailed          = "redeem.failed"
	MsgCreateDefaultTokenErr = "user.create_default_token_error"
	MsgUuidDuplicate         = "common.uuid_duplicate"
	MsgInvalidInput          = "common.invalid_input"
)

// Distributor related messages
const (
	MsgDistributorInvalidRequest          = "distributor.invalid_request"
	MsgDistributorInvalidChannelId        = "distributor.invalid_channel_id"
	MsgDistributorChannelDisabled         = "distributor.channel_disabled"
	MsgDistributorAffinityChannelDisabled = "distributor.affinity_channel_disabled"
	MsgDistributorTokenNoModelAccess      = "distributor.token_no_model_access"
	MsgDistributorTokenModelForbidden     = "distributor.token_model_forbidden"
	MsgDistributorModelNameRequired       = "distributor.model_name_required"
	MsgDistributorInvalidPlayground       = "distributor.invalid_playground_request"
	MsgDistributorGroupAccessDenied       = "distributor.group_access_denied"
	MsgDistributorGetChannelFailed        = "distributor.get_channel_failed"
	MsgDistributorNoAvailableChannel      = "distributor.no_available_channel"
	MsgDistributorInvalidMidjourney       = "distributor.invalid_midjourney_request"
	MsgDistributorInvalidParseModel       = "distributor.invalid_request_parse_model"
)

// Custom OAuth provider related messages
const (
	MsgCustomOAuthNotFound          = "custom_oauth.not_found"
	MsgCustomOAuthSlugEmpty         = "custom_oauth.slug_empty"
	MsgCustomOAuthSlugExists        = "custom_oauth.slug_exists"
	MsgCustomOAuthNameEmpty         = "custom_oauth.name_empty"
	MsgCustomOAuthHasBindings       = "custom_oauth.has_bindings"
	MsgCustomOAuthBindingNotFound   = "custom_oauth.binding_not_found"
	MsgCustomOAuthProviderIdInvalid = "custom_oauth.provider_id_field_invalid"
	MsgCustomOAuthDiscoveryRequired = "custom_oauth.discovery_required"
	MsgCustomOAuthDiscoveryURL      = "custom_oauth.discovery_url_invalid"
	MsgCustomOAuthDiscoveryCreate   = "custom_oauth.discovery_request_failed"
	MsgCustomOAuthDiscoveryFetch    = "custom_oauth.discovery_fetch_failed"
	MsgCustomOAuthDiscoveryParse    = "custom_oauth.discovery_parse_failed"
	MsgCustomOAuthSlugConflict      = "custom_oauth.slug_builtin_conflict"
	MsgCustomOAuthBindingCheck      = "custom_oauth.binding_check_failed"
)

// Administration and promotion messages
const (
	MsgAdminRootRequired                = "admin.root_required"
	MsgDistributorDiscountInvalid       = "distributor.discount_invalid"
	MsgInvitePromoInvalidEvent          = "invite_promo.invalid_event"
	MsgModelPricingOfficialPriceMissing = "model_pricing.official_price_missing"
	MsgModelPricingChannelRowNotFound   = "model_pricing.channel_row_not_found"
)
