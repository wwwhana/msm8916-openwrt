'use strict';
'require baseclass';
'require rpc';
'require fs';

var callStatus = rpc.declare({ object: 'a8-network', method: 'status', expect: {} });
return baseclass.extend({
    status: function() {
        return L.resolveDefault(callStatus(), null).then(function(data) {
            if (data && data.usb) return data;
            return L.resolveDefault(fs.read('/tmp/uplink.json'), '').then(function(text) {
                try { return { uplink: JSON.parse(text) }; }
                catch (e) { return { uplink: {} }; }
            });
        });
    }
});
