// carousel.js — status-bar carousel rotation for the provider-quota plugin.
//
// When several providers have quota data, the status segment cycles through
// them every few seconds. The carousel only reorders/renders cached data — it
// never triggers a fetch (fetches are owned by the refresh scheduler in
// plugin.js).

// Carousel holds rotation state.
function Carousel(rotateMs) {
	this.rotateMs = rotateMs || 3000;
	this.idx = 0;
	this.timerId = null;
}

// start begins rotation. onTick is called each rotate step and should return
// the provider-id list currently worth showing (the carousel advances within
// that list and re-renders via the caller's refresh hook).
Carousel.prototype.start = function(onTick) {
	var self = this;
	if (this.timerId !== null) {
		return; // already running
	}
	this.timerId = goa.setInterval(function() {
		var count = onTick(self.idx);
		if (count > 1) {
			self.idx = (self.idx + 1) % count;
		}
	}, this.rotateMs);
};

// current returns the index into the provider list to display.
Carousel.prototype.current = function() {
	return this.idx;
};

// reset returns the carousel to the first provider.
Carousel.prototype.reset = function() {
	this.idx = 0;
};

// stop halts rotation.
Carousel.prototype.stop = function() {
	if (this.timerId !== null) {
		goa.clearInterval(this.timerId);
		this.timerId = null;
	}
};

exports.Carousel = Carousel;
