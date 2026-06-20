package llm

// BankingTools returns the full set of tools the banking agent can invoke.
// Each description is written for the LLM — precise wording matters.
func BankingTools() []Tool {
	return []Tool{
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "get_wallet_balance",
				Description: "Get the user's Weave wallet balance (available and total). Call this whenever the user asks about their balance or before deciding if they have enough funds.",
				Parameters:  emptyParams(),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "get_linked_banks",
				Description: "List all bank accounts the user has linked to Weave, including balances and sourcing priority.",
				Parameters:  emptyParams(),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "get_transfer_history",
				Description: "Retrieve the user's recent outgoing transfers.",
				Parameters: objectParams(map[string]interface{}{
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Number of transfers to return (default 5, max 20).",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"PENDING", "COMPLETED", "FAILED", ""},
						"description": "Optional status filter.",
					},
				}, nil),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "get_transfer_status",
				Description: "Get the status and details of a specific transfer by its reference (e.g. WVF-abc123).",
				Parameters: objectParams(map[string]interface{}{
					"reference": map[string]interface{}{
						"type":        "string",
						"description": "The transfer reference starting with WVF-.",
					},
				}, []string{"reference"}),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "lookup_account",
				Description: "Resolve a Nigerian bank account number to get the account holder's name. Use this to verify the recipient before initiating a transfer.",
				Parameters: objectParams(map[string]interface{}{
					"account_number": map[string]interface{}{
						"type":        "string",
						"description": "10-digit Nigerian bank account number.",
					},
					"bank": map[string]interface{}{
						"type":        "string",
						"description": "Bank name (e.g. GTBank, Access Bank, Zenith Bank).",
					},
				}, []string{"account_number", "bank"}),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "initiate_transfer",
				Description: "Build and store a transfer plan. This does NOT move any money — it creates a pending transfer that requires the user to explicitly confirm. Always call this first, then present the plan to the user, then wait for them to say yes before calling confirm_transfer.",
				Parameters: objectParams(map[string]interface{}{
					"amount": map[string]interface{}{
						"type":        "number",
						"description": "Amount in naira (not kobo).",
					},
					"recipient_account": map[string]interface{}{
						"type":        "string",
						"description": "10-digit recipient account number.",
					},
					"recipient_bank": map[string]interface{}{
						"type":        "string",
						"description": "Recipient bank name.",
					},
					"recipient_name": map[string]interface{}{
						"type":        "string",
						"description": "Recipient name, if known.",
					},
				}, []string{"amount", "recipient_account", "recipient_bank"}),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "confirm_transfer",
				Description: "Execute the pending transfer that was set up by initiate_transfer. Only call this after the user has explicitly confirmed (said yes, proceed, confirm, etc.).",
				Parameters:  emptyParams(),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "cancel_transfer",
				Description: "Cancel and clear any pending transfer or unlink action.",
				Parameters:  emptyParams(),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "get_wallet_account",
				Description: "Get the user's Weave virtual account details for receiving funds (bank name, account number, account name).",
				Parameters:  emptyParams(),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "refresh_bank_balance",
				Description: "Fetch the latest live balance from the bank provider for one or all linked banks.",
				Parameters: objectParams(map[string]interface{}{
					"bank_identifier": map[string]interface{}{
						"type":        "string",
						"description": "Bank name or partial account number to identify which bank. Leave empty to refresh all.",
					},
				}, nil),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "update_bank_priority",
				Description: "Change the sourcing priority of a linked bank. Priority 1 is used first when funding a transfer.",
				Parameters: objectParams(map[string]interface{}{
					"bank_identifier": map[string]interface{}{
						"type":        "string",
						"description": "Bank name or partial account number.",
					},
					"priority": map[string]interface{}{
						"type":        "integer",
						"description": "New priority (1 = highest, 5 = lowest).",
					},
				}, []string{"bank_identifier", "priority"}),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "initiate_unlink",
				Description: "Stage a bank account for unlinking. Like initiate_transfer, this requires user confirmation before the bank is actually removed.",
				Parameters: objectParams(map[string]interface{}{
					"bank_identifier": map[string]interface{}{
						"type":        "string",
						"description": "Bank name or partial account number to identify which bank to unlink.",
					},
				}, []string{"bank_identifier"}),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "confirm_unlink",
				Description: "Execute the pending unlink after the user has confirmed.",
				Parameters:  emptyParams(),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "get_wallet_history",
				Description: "Get the user's recent wallet deposits (money received into Weave wallet).",
				Parameters: objectParams(map[string]interface{}{
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Number of records to return (default 5).",
					},
				}, nil),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "fund_wallet",
				Description: "Top up the user's Weave wallet by pulling funds from a linked bank account via Mono direct debit. Use when the user asks to add money to their wallet from a linked bank, or when a transfer fails due to insufficient funds and the user agrees to fund the wallet first.",
				Parameters: objectParams(map[string]interface{}{
					"bank_identifier": map[string]interface{}{
						"type":        "string",
						"description": "Bank name or partial account number to identify which linked bank account to debit (e.g. 'Keystone', 'GTBank', '0131883').",
					},
					"amount_ngn": map[string]interface{}{
						"type":        "number",
						"description": "Amount to pull into the wallet in NGN (e.g. 10000 for ₦10,000).",
					},
				}, []string{"bank_identifier", "amount_ngn"}),
			},
		},
	}
}

func emptyParams() interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func objectParams(props map[string]interface{}, required []string) interface{} {
	p := map[string]interface{}{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		p["required"] = required
	}
	return p
}
