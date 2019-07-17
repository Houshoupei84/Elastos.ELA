-- Copyright (c) 2017-2019 Elastos Foundation
-- Use of this source code is governed by an MIT
-- license that can be found in the LICENSE file.
-- 

deposit_address_arg = "--deposit_address"
cr_publickey_arg = "--cr_publickey_arg"
nick_name_arg  = "--nick_name_arg"
url_arg = "--url_arg"
location_arg = "--location_arg"

arg_name_ary = {deposit_address_arg, cr_publickey_arg, nick_name_arg,
                url_arg, location_arg}


local m = require("api")

function print_arg()
    print("begin print_arg")
    for i, v in pairs(arg) do
        print(i, v)
    end
end



function print_help()

    print("help----------")
end

--[[
function table_length(t)
    local length = 0
    for k, v in pairs(t) do
        length = length+1
    end
    return length
end

function is_arg_invalid()
    local length = 0
    length = table_length(arg)


end

function is_arg_invalid()
    local length = 0
    length = table_length(arg)
end
]]

local deposit_address
local cr_publickey
local nick_name
local url
local location

function is_not_have_arg(argname)
    if arg[argname] == nil then
        return true
    end
    return false
end

function check_arg()
    print("check_arg----------begin"..#arg_name_ary)
    for k, v in pairs(arg_name_ary) do
        print(k, v)
        if is_not_have_arg(v) then
            print_help()
            return false
        end
    end

    return true
end

function get_arg()
    print("get_arg----------begin")
    deposit_address = arg["--deposit_address"]
    cr_publickey = arg["--cr_publickey_arg"]
    nick_name = arg["--nick_name_arg"]
    url = arg["--url_arg"]
    location = arg["--location_arg"]
end

print_arg()
is_ok = check_arg()

if is_ok == false then
    print("inValid return")
    return
end
get_arg()


-- client: path, password, if create
local wallet = client.new("keystore.dat", "123", false)

-- account
local addr = wallet:get_address()
local pubkey = wallet:get_publickey()
print(addr)
print(pubkey)

-- asset_id
local asset_id = m.get_asset_id()

-- amount, fee
local amount = 5000
local fee = 0.001



-- register cr payload: publickey, nickname, url, local, wallet
local rp_payload =registercr.new(cr_publickey, nick_name, url, location, wallet)
print(rp_payload:get())

-- transaction: version, txType, payloadVersion, payload, locktime
local tx = transaction.new(9, 0x21, 0, rp_payload, 0)
print(tx:get())

-- input: from, amount + fee
local charge = tx:appendenough(addr, (amount + fee) * 100000000)
print(charge)

-- outputpayload
local default_output = defaultoutput.new()

-- output: asset_id, value, recipient, output_paload_type, outputpaload
local charge_output = output.new(asset_id, charge, addr, 0, default_output)

local amount_output = output.new(asset_id, amount * 100000000, deposit_address, 0, default_output)


tx:appendtxout(charge_output)
tx:appendtxout(amount_output)

-- sign
tx:sign(wallet)
print(tx:get())

-- send
local hash = tx:hash()
local res = m.send_tx(tx)

print("sending " .. hash)

if (res ~= hash)
then
    print(res)
else
    print("tx send success")
end
